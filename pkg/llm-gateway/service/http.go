package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/valyala/fastjson"
	"golang.org/x/sync/errgroup"
	"k8s.io/klog/v2"

	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/lrs"
	"easgo/pkg/llm-gateway/metrics"
	"easgo/pkg/llm-gateway/protocol"
	"easgo/pkg/llm-gateway/resolver"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
	"easgo/pkg/util/jsquery"
)

const (
	ReadBufferSize    = 8 * 1024
	MaxReadBufferSize = 100 * 1024 * 1024
	ReadTimeout       = 5 * time.Minute
)

func streamCopyProcessedData(req *structs.Request, src io.ReadCloser, rspProcessor func([]byte, bool) []byte, wfn func([]byte)) (wErr error, rErr error) {
	timeoutSrc := NewSSEReaderWithTimeout(src, ReadTimeout)
	buf := make([]byte, ReadBufferSize)

	dst := req.Writer
	isFirstChunk := true
	for {
		n, err := timeoutSrc.Read(buf)
		if err != nil {
			if err == io.EOF {
				wErr, rErr = nil, nil
			} else if errors.Is(err, ErrBufferTooSmall) && n <= MaxReadBufferSize {
				klog.V(3).Infof("expanding buffer from %d to %d bytes", len(buf), n)
				buf = make([]byte, n)
				continue
			} else {
				klog.Errorf("error while reading response SSE: %v\n", err)
				wErr, rErr = nil, err
			}
			goto EndLoop
		}
		processedData := rspProcessor(buf[0:n], isFirstChunk)
		isFirstChunk = false
		klog.V(3).Infof("streamCopyTokenizedChat: %s", string(processedData))

		nw, ew := dst.Write(processedData)
		if wfn != nil {
			wfn(processedData)
		}
		if ew != nil {
			wErr, rErr = ew, nil
			goto EndLoop
		}
		if len(processedData) != nw {
			wErr, rErr = io.ErrShortWrite, nil
			goto EndLoop
		}

		if req.OutputExceedMaxTokens() {
			goto EndLoop
		}
	}

EndLoop:
	timeoutSrc.Close()
	return
}

func streamCopy(dst io.Writer, src io.ReadCloser, wfn func([]byte), stream bool) (wErr error, rErr error) {
	buf := make([]byte, ReadBufferSize)
	timeoutSrc := src
	if stream {
		timeoutSrc = NewSSEReaderWithTimeout(src, ReadTimeout)
	}
	for {
		nr, er := timeoutSrc.Read(buf)
		if errors.Is(er, ErrBufferTooSmall) && nr <= MaxReadBufferSize {
			klog.V(3).Infof("expanding buffer from %d to %d bytes", len(buf), nr)
			buf = make([]byte, nr)
			continue
		}
		if nr > 0 {
			data := buf[0:nr]
			wData := data
			if stream {
				wData = utils.BuildSSEData(data)
			}
			nw, ew := dst.Write(wData)
			if wfn != nil {
				wfn(data)
			}
			if ew != nil {
				return ew, nil
			}
			if len(wData) != nw {
				return io.ErrShortWrite, nil
			}
		}
		if er != nil {
			if er == io.EOF {
				return nil, nil
			} else {
				return nil, er
			}
		}
	}
}

func (hs *LlmGatewayService) doHttpErrorResponse(req *structs.Request, statusCode int, text string) {
	if !req.HeaderResponded && req.FallbackAttempt < hs.sr.GetFallbackLength() {
		nextTokens, err := hs.sr.GetFallbackTokens(req)
		if err != nil {
			klog.Errorf("get fallback tokens failed: %v", err)
			return
		}
		req.Token, req.SecondToken = nil, nil
		req.ExternalEp = nextTokens.ExternalEp

		hs.handleProxy(req, false)
		return
	}

	if !req.Retrying {
		if !req.HeaderResponded {
			http.Error(req.Writer, text, statusCode)
			req.HeaderResponded = true
		} else {
			fmt.Fprintln(req.Writer, text)
		}

		if req.StatusCode == 0 || req.StatusCode == http.StatusOK {
			req.StatusCode = statusCode
		}
		hs.LogRequestAccess(req)
	}
}

func (hs *LlmGatewayService) EnableMirrorRequest() bool {
	propertyManager := hs.config.GetConfigManager()
	if propertyManager == nil {
		return false
	}
	return propertyManager.GetBoolWithDefault("llm_gateway.traffic_mirror.enable", false)
}

// MirrorRequest sends a copy of the request to the mirror target based on the configured ratio
func (hs *LlmGatewayService) MirrorRequest(req *structs.Request) {
	// Perform the mirror request in a separate goroutine to avoid blocking
	go func() {
		defer func() {
			if r := recover(); r != nil {
				klog.Errorf("Panic in mirror request cloning: %v", r)
			}
		}()

		// Get the newest mirror configuration directly from property manager
		propertyManager := hs.config.GetConfigManager()
		mirrorTarget := propertyManager.GetStringWithDefault("llm_gateway.traffic_mirror.target", "")
		mirrorRatio := propertyManager.GetFloatWithDefault("llm_gateway.traffic_mirror.ratio", 0.0)
		mirrorToken := propertyManager.GetStringWithDefault("llm_gateway.traffic_mirror.token", "")
		mirrorTimeout := propertyManager.GetFloatWithDefault("llm_gateway.traffic_mirror.timeout", 0.0)
		mirrorLog := propertyManager.GetBoolWithDefault("llm_gateway.traffic_mirror.enable_log", false)
		if mirrorLog {
			klog.Infof("[%s] Mirror request enabled, target: %s, ratio: %.2f, token: %s, timeout: %.2f", req.Id, mirrorTarget, mirrorRatio, mirrorToken, mirrorTimeout)
		}

		// If mirror target is not configured or ratio is not positive, skip mirroring
		if mirrorTarget == "" || mirrorRatio <= 0 {
			return
		}

		// Check if we should mirror this request based on the ratio
		if float64(rand.Intn(100)) >= mirrorRatio {
			return
		}

		mirrorURL := fmt.Sprintf("%s%s", mirrorTarget, req.Req.URL.Path)
		mirrorReq, err := http.NewRequest(
			req.Req.Method,
			mirrorURL,
			bytes.NewReader(req.RawData),
		)
		if err != nil {
			if mirrorLog {
				klog.Errorf("Failed to create mirror request: %v", err)
			}
			return
		}

		mirrorReq.Header = req.Req.Header.Clone()
		mirrorReq.Header.Del("Content-Length")

		if mirrorToken != "" {
			mirrorReq.Header.Set("Authorization", mirrorToken)
		}
		if mirrorTimeout > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(mirrorTimeout)*time.Millisecond)
			defer cancel()
			mirrorReq = mirrorReq.WithContext(ctx)
		}

		client := &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout: 1 * time.Second,
				}).DialContext,
			},
		}
		resp, err := client.Do(mirrorReq)
		if err != nil {
			if mirrorLog {
				klog.Errorf("Mirror request failed: %v", err)
			}
			return
		}
		defer resp.Body.Close()

		_, _ = streamCopy(io.Discard, resp.Body, nil, req.Stream)

		if mirrorLog {
			klog.Infof("[%s] Mirror request sent to %s, status: %d", req.Id, mirrorURL, resp.StatusCode)
		}
	}()
}

func (hs *LlmGatewayService) makeNewHttpRequest(req *structs.Request, data []byte, endpoint *structs.Endpoint, ctx context.Context) (*http.Request, error) {
	var url string
	if endpoint != nil {
		url = fmt.Sprintf("http://%s%s", endpoint.String(), req.Req.URL.String())
	} else {
		url = req.ExternalEp.JoinURL(req.Req.URL.String())
	}
	var (
		newReq *http.Request
		err    error
	)
	if ctx != nil {
		newReq, err = http.NewRequestWithContext(ctx, req.Req.Method, url, bytes.NewBuffer(data))
	} else {
		newReq, err = http.NewRequest(req.Req.Method, url, bytes.NewBuffer(data))
	}
	if err != nil {
		return nil, fmt.Errorf("make new http request %s:%s error: %s", req.Req.Method, url, err)
	}

	// copy origin http header
	httpRequest := req.Req
	newReq.Header = httpRequest.Header.Clone()
	newReq.ContentLength = int64(len(data))

	newReq.Header.Del("content-length")
	utils.DelHopHeaders(newReq.Header)
	clientIP, _, err := net.SplitHostPort(httpRequest.RemoteAddr)
	if err == nil {
		utils.AppendHostToXForwardHeader(newReq.Header, clientIP)
		req.DownStream = clientIP
	}
	newReq.Header.Set("x-proxy-server", "llm-gateway")

	if len(hs.config.BackendServiceToken) > 0 {
		newReq.Header.Set("authorization", hs.config.BackendServiceToken)
	}
	if req.ExternalEp != nil {
		newReq.Header.Set("Authorization", "Bearer "+req.ExternalEp.APIKey)
		newReq.Header.Set("Content-Type", "application/json")
	}

	return newReq, nil
}

func (hs *LlmGatewayService) doHttpRequest(newFunc func() (*http.Request, error)) (*http.Response, *http.Request, error) {
	nRetry := 0
RETRY:
	httpReq, err := newFunc()
	if err != nil {
		return nil, nil, err
	}
	resp, err := hs.client.Do(httpReq)
	if err != nil {
		klog.Warningf("request to service %s error: %v, retry: %v", httpReq.URL.String(), err, nRetry)
		// if the error is not timeout, will retry
		var netErr net.Error
		if !(errors.As(err, &netErr) && netErr.Timeout()) {
			if nRetry < 3 {
				nRetry++
				time.Sleep(1 * time.Millisecond)
				goto RETRY
			}
		}
		return nil, nil, err
	}
	return resp, httpReq, nil
}

func (hs *LlmGatewayService) makeFirstStreamCompletionsResponse(req *structs.Request, prefillResponse []byte) (*protocol.CompletionResponse, error) {
	prefillResponseObj := new(protocol.PDSplitCompletionsPrefillResponse)
	err := json.Unmarshal(prefillResponse, prefillResponseObj)
	if err != nil {
		return nil, fmt.Errorf("unmarshal the prefill response: %v", string(prefillResponse))
	}
	req.PrefillResponse = prefillResponseObj

	streamCompletionsResponseChoice := protocol.CompletionChoice{
		Index: prefillResponseObj.Data[0].Index,
		Text:  prefillResponseObj.Data[0].FirstText,
	}
	if prefillResponseObj.Data[0].LogProbs != nil {
		streamCompletionsResponseChoice.LogProbs = *prefillResponseObj.Data[0].LogProbs
	}
	streamCompletionsResponseChoice.FinishReason = prefillResponseObj.Data[0].FinishReason

	streamCompletionsResponse := &protocol.CompletionResponse{
		ID:      prefillResponseObj.ID,
		Object:  prefillResponseObj.Object,
		Created: prefillResponseObj.Created,
		Model:   prefillResponseObj.Model,
		Choices: []protocol.CompletionChoice{streamCompletionsResponseChoice},
	}
	if prefillResponseObj.Usage != nil {
		streamCompletionsResponse.Usage = prefillResponseObj.Usage
	}
	return streamCompletionsResponse, nil
}

func (hs *LlmGatewayService) makeFirstStreamChatResponse(req *structs.Request, prefillResponse []byte) (*protocol.ChatCompletionStreamResponse, error) {
	prefillResponseObj := new(protocol.PDSplitCompletionsPrefillResponse)
	err := json.Unmarshal(prefillResponse, prefillResponseObj)
	if err != nil {
		return nil, fmt.Errorf("unmarshal the prefill response: %v", string(prefillResponse))
	}
	req.PrefillResponse = prefillResponseObj

	streamCompletionsResponseChoice := protocol.ChatCompletionStreamChoice{
		Index:        prefillResponseObj.Data[0].Index,
		Delta:        protocol.ChatCompletionStreamChoiceDelta{Content: prefillResponseObj.Data[0].FirstText},
		FinishReason: prefillResponseObj.Data[0].FinishReason,
	}

	streamCompletionsResponse := &protocol.ChatCompletionStreamResponse{
		ID:      prefillResponseObj.ID,
		Object:  prefillResponseObj.Object,
		Created: prefillResponseObj.Created,
		Model:   prefillResponseObj.Model,
		Choices: []protocol.ChatCompletionStreamChoice{streamCompletionsResponseChoice},
		Usage:   prefillResponseObj.Usage,
	}
	return streamCompletionsResponse, nil
}

func (hs *LlmGatewayService) handlePrefillResponse(req *structs.Request, httpReq *http.Request, httpResp *http.Response, respBody []byte) error {
	writer := req.Writer
	klog.V(3).Infof("prefill response body: %v", string(respBody))

	// function makeXXX will set req.PrefillResponse
	var resp []byte
	switch req.Protocol {
	case protocol.ProtocolCompletions:
		completions, err := hs.makeFirstStreamCompletionsResponse(req, respBody)
		if err != nil {
			return fmt.Errorf("make first stream completions response failed: %v", err)
		}
		resp, _ = json.Marshal(completions)
	case protocol.ProtocolChat:
		chatCompletions, err := hs.makeFirstStreamChatResponse(req, respBody)
		if err != nil {
			return fmt.Errorf("make first stream chat response failed: %v", err)
		}
		resp, _ = json.Marshal(chatCompletions)
	default:
		return fmt.Errorf("not support this protocol: %v", req.Protocol)
	}

	// write response
	if req.Stream && !req.HeaderResponded {
		// sse header
		httpResp.Header.Set("Content-Type", "text/event-stream")
		httpResp.Header.Set("Connection", "keep-alive")

		httpResp.Header.Del("Content-Length")
		utils.DelHopHeaders(httpResp.Header)
		utils.CopyHeader(writer.Header(), httpResp.Header)
		writer.WriteHeader(httpResp.StatusCode)
		req.HeaderResponded = true

		writer.Write([]byte("data: "))
		writer.Write(resp)
		writer.Write([]byte("\n\n"))
		writer.(http.Flusher).Flush()
	}

	// log metric
	req.FirstTime = time.Now()
	firstCost := req.FirstTime.Sub(req.EnQueueTime)
	metrics.Latency("llm_ttft", req.DefaultLabels).Add(firstCost.Milliseconds())

	return nil
}

func (hs *LlmGatewayService) handleRetry(req *structs.Request, needRecord bool, nCopy int, recordedMsg [][]byte) {
	if req.ReScheduleCount >= hs.config.RetryCount {
		return
	}
	req.ReScheduleCount += 1
	if !needRecord && nCopy == 0 {
		hs.tryNext(req)
		return
	}

	// record msg
	if nCopy == 0 {
		hs.tryNext(req)
		return
	}

	// len(recordedMsg) > 0, need merge to orig request
	if newBody, err := utils.MergeLLMReqBodyWithContent(req.Token.InferBackend, req.Req, req.Data, &recordedMsg); err == nil {
		req.Data = newBody
		hs.tryNext(req)
	} else {
		klog.Errorf("merge llm request error: %v", err)
	}
}

func (hs *LlmGatewayService) handleDecodeStreamResponse(req *structs.Request, body io.ReadCloser, token *structs.Token, needRecord bool, inferMode string) error {
	if req.InferStream && hs.config.EnableRequestReport() {
		// setup request state, the token.Endpoint will must decode
		gatewayEp := &structs.GatewayEndpoint{Endpoint: req.GatewayEp}
		reqState := lrs.NewReqTokenState(req, token.Model, consts.DecodeInferMode, token.Id(), gatewayEp.Id())
		req.RequestState = reqState
		hs.requestReporter.AddReqTokenState(reqState)
		// clear req token state when request done
		defer hs.requestReporter.DeleteReqTokenState(req.Id)
	}

	nCopy := 0
	var lastCopyTime time.Time
	var recordedMsg [][]byte

	copyHandler := func(data []byte) {
		if nCopy == 0 {
			switch inferMode {
			case consts.DecodeInferMode:
				lastCopyTime = time.Now()
				req.Itls = append(req.Itls, lastCopyTime.Sub(req.FirstTime).Milliseconds())
			case consts.NormalInferMode:
				// In the scenario of PD, after the first packet is processed, the P-node no longer performs any calculations.
				// therefore, it should release its resources upon receiving the first packet.
				if req.SecondToken != nil {
					hs.lb.ReleaseToken(req, req.Token)
				}
				req.FirstTime = time.Now()
				lastCopyTime = req.FirstTime
				firstCost := req.FirstTime.Sub(req.EnQueueTime)
				metrics.Latency("llm_ttft", req.DefaultLabels).Add(firstCost.Milliseconds())
			default:
				klog.Warning("stream copy: not support this infer mode.")
			}
		} else {
			now := time.Now()
			timeOutput := now.Sub(lastCopyTime)
			req.Itls = append(req.Itls, timeOutput.Milliseconds())
			lastCopyTime = now
		}
		nCopy += 1

		if needRecord {
			recordedMsg = append(recordedMsg, append([]byte(nil), data...))
		}

		req.Writer.(http.Flusher).Flush()
	}

	// wErr indicates an error occurred while writing back to the client
	// rErr indicates an error occurred while reading from the backend service
	var wErr, rErr error

	// TODO: now can not detect client socket closed or not, if read backend service timeout.
	if req.RequestProcessor != nil && req.RequestProcessor.IsApplicable(req) {
		contentProcessor := func(data []byte, isFirstChunk bool) []byte {
			if req.InferStream && hs.config.EnableRequestReport() {
				req.ParseResponse(data) // parse response and convert it to completion object
				req.UpdateTokensState(data)
			}
			statusCode := http.StatusOK
			if req.Stream {
				bytesToWrite, err := req.RequestProcessor.PostProcessStream(req, data, isFirstChunk)
				if isFirstChunk {
					if errors.Is(err, consts.ErrorRequestAbortedByEngine) {
						klog.Errorf("[%v] post process stream failed: %v", req.Id, err)
						statusCode = http.StatusBadRequest
					}
					hs.doResponseHeader(req, statusCode, req.ResponseHeader)
				}
				return bytesToWrite
			} else {
				bytesToWrite, err := req.RequestProcessor.BufferStreamToResp(req, data, isFirstChunk)
				if isFirstChunk {
					if errors.Is(err, consts.ErrorRequestAbortedByEngine) {
						klog.Errorf("[%v] post buffer stream failed: %v", req.Id, err)
						statusCode = http.StatusBadRequest
					}
					hs.doResponseHeader(req, statusCode, req.ResponseHeader)
				}
				return bytesToWrite
			}
		}
		wErr, rErr = streamCopyProcessedData(req, body, contentProcessor, copyHandler)
	} else {
		hs.doResponseHeader(req, req.StatusCode, req.ResponseHeader)
		copyFunc := func(data []byte) {
			copyHandler(data)
			if req.InferStream && hs.config.EnableRequestReport() {
				req.ParseResponse(data)
				req.UpdateTokensState(data)
			}
		}
		wErr, rErr = streamCopy(req.Writer, body, copyFunc, req.InferStream)
	}
	metrics.Latency("llm_tpot", req.DefaultLabels).AddMany(req.Itls)

	// stream copy successfully
	if wErr == nil && rErr == nil {
		return nil
	}

	// do not handle the request while client disconnected
	if wErr != nil {
		klog.Warningf("[%s] stream writing back to client error: %v", req.Id, wErr)
		req.StatusCode = consts.StatusDisConnection
		return wErr
	}

	// server error, but client is ok, try next server may solve this problem.
	if rErr != nil {
		klog.Warningf("[%s] stream read from server error: %v, retry: %d", req.Id, rErr, req.ReScheduleCount)
		if rErr != consts.ErrorReadTimeout {
			hs.handleRetry(req, needRecord, nCopy, recordedMsg)
		}
		return rErr
	}
	return nil
}

func (hs *LlmGatewayService) handleDecodeResponse(req *structs.Request, resp *http.Response) error {
	var err error
	decodeResponseData, err := io.ReadAll(resp.Body)
	if err != nil {
		klog.Errorf("read decode from infer backend error: %v, retry: %d", err, req.ReScheduleCount)
		if req.ReScheduleCount < hs.config.RetryCount {
			req.ReScheduleCount++
			hs.tryNext(req)
		}
		return err
	}

	var respData []byte
	prefillResponse := req.PrefillResponse
	switch req.Protocol {
	case protocol.ProtocolCompletions:
		respData, err = hs.mergePrefillDecodeCompletionsResponse(prefillResponse, decodeResponseData)
	case protocol.ProtocolChat:
		respData, err = hs.mergePrefillDecodeChatCompletionsResponse(prefillResponse, decodeResponseData)
	}
	if err != nil {
		return err
	}

	writer := req.Writer
	resp.Header.Del("content-length")
	resp.ContentLength = int64(len(respData))
	utils.DelHopHeaders(resp.Header)
	utils.CopyHeader(writer.Header(), resp.Header)
	writer.WriteHeader(resp.StatusCode)
	req.HeaderResponded = true
	_, err = req.Writer.Write(respData)
	if err != nil {
		klog.Errorf("write decode response failed: %v", err)
	}
	return err
}

func (hs *LlmGatewayService) handlePrefillRequest(req *structs.Request, ignoreResponse bool) (err error) {
	// release prefill instance resource
	defer func() {
		hs.lb.ReleaseToken(req, req.Token)
		if err != nil && utils.IsRequestConnected(req.Req) {
			hs.doHttpErrorResponse(req, http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
		}
	}()
	tStart := time.Now()
	// detect if the client has disconnected
	if !utils.IsRequestConnected(req.Req) {
		klog.Warningf("[%s] client disconnected before processing, error: %v", req.Id, req.Req.Context().Err())
		req.StatusCode = consts.StatusDisConnection
		hs.LogRequestAccess(req)
		return fmt.Errorf("request is not connected")
	}

	// prefill token
	token := req.Token
	defer hs.lb.ReleaseToken(req, token)

	var reqData []byte
	if hs.config.IsVllmMooncakeSplitMode() {
		hs.BuildVllmMooncakePrefillBody(req)
		reqData = req.Data
	} else {
		// set the max_token = 1
		var reqObj map[string]interface{}
		if err := json.Unmarshal(req.Data, &reqObj); err != nil {
			return fmt.Errorf("unmarshal json: %v; input: %v", err, string(req.Data))
		}
		reqObj["max_tokens"] = 1
		reqObj["request_id"] = req.Id
		reqObj["prefill_only"] = true
		reqData, _ = json.Marshal(reqObj)
	}

	// create prefill request
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	newFunc := func() (*http.Request, error) {
		newReq, err := hs.makeNewHttpRequest(req, reqData, &token.Endpoint, ctx)
		if err != nil {
			return nil, fmt.Errorf("make http request failed: %v", err)
		}
		return newReq, nil
	}
	// do http request
	// http.Client.Do will be blocked until the first packet is received, so for convenience, we record this request here.
	resp, newReq, err := hs.doHttpRequest(newFunc)
	if err != nil {
		return fmt.Errorf("do http request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad response status code: %d", resp.StatusCode)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if hs.config.IsVllmMooncakeSplitMode() {
		// Parse response to extract kv_transfer_params
		var respObj map[string]interface{}
		if err := json.Unmarshal(respBody, &respObj); err == nil {
			if kvParams, ok := respObj["kv_transfer_params"].(map[string]interface{}); ok {
				req.KVTransferParams = kvParams
			}
		}
	}
	if !ignoreResponse {
		// handle response body
		err = hs.handlePrefillResponse(req, newReq, resp, respBody)
	}
	req.PrefillTime = time.Now()
	klog.V(3).Infof("[%s] do prefill (%s) finish %dms", req.Id, req.Token.Endpoint.String(), time.Since(tStart).Milliseconds())
	return
}

func (hs *LlmGatewayService) mergePrefillDecodeCompletionsResponse(
	prefillResponse *protocol.PDSplitCompletionsPrefillResponse, decodeResponseData []byte) ([]byte, error) {
	var decodeResponseObj protocol.CompletionResponse
	if err := json.Unmarshal(decodeResponseData, &decodeResponseObj); err != nil {
		klog.Error("unmarshal decode response failed: %v", string(decodeResponseData))
		return nil, err
	}

	decodeResponseObj.Created = prefillResponse.Created

	// TODO
	firstText := prefillResponse.Data[0].FirstText
	text := decodeResponseObj.Choices[0].Text
	decodeResponseObj.Choices[0].Text = firstText + text

	data, err := json.Marshal(decodeResponseObj)
	if err != nil {
		klog.Errorf("marshal decode response failed: %v, %v", err, decodeResponseObj)
	}
	return data, err
}

func (hs *LlmGatewayService) mergePrefillDecodeChatCompletionsResponse(
	prefillResponse *protocol.PDSplitCompletionsPrefillResponse, decodeResponseData []byte) ([]byte, error) {
	var decodeResponseObj protocol.ChatCompletionResponse
	if err := json.Unmarshal(decodeResponseData, &decodeResponseObj); err != nil {
		klog.Error("unmarshal decode response failed: %v", string(decodeResponseData))
		return nil, err
	}

	decodeResponseObj.Created = prefillResponse.Created

	// TODO
	firstText := prefillResponse.Data[0].FirstText
	text := decodeResponseObj.Choices[0].Message.Content
	decodeResponseObj.Choices[0].Message.Content = firstText + text

	data, err := json.Marshal(decodeResponseObj)
	if err != nil {
		klog.Errorf("marshal decode response failed: %v, %v", err, decodeResponseObj)
	}
	return data, err
}

func (hs *LlmGatewayService) asyncHandleProxyPD(req *structs.Request) {
	// make modified request data
	if req.Data == nil {
		hs.doHttpErrorResponse(req, http.StatusBadRequest, "failed to parse request body")
		return
	}

	if req.Token == nil || req.SecondToken == nil {
		hs.doHttpErrorResponse(req, http.StatusBadRequest, "failed to get token")
		return
	}

	klog.Infof("%v proxy pd to prefill:%v, decode:%v", req.Id, req.Token.WorkerId, req.SecondToken.WorkerId)
	obj := fastjson.MustParseBytes(req.Data)
	bootstrapRoom := rand.Intn(1<<63 - 1)
	if req.SecondToken.WorkerId != "" {
		parser := resolver.GetParser(hs.config.PDSplitMode)
		dpRank, dpSize, err := parser.ParseDPRankSize(req.SecondToken.WorkerId)
		if err != nil {
			klog.Warningf("%v failed to parse dp rank size by %v, err: %v", req.Id, req.SecondToken.WorkerId, err)
		} else {
			bootstrapRoom = bootstrapRoom/dpSize*dpSize + dpRank
			if dpSize > 1 {
				obj.Set("data_parallel_rank", req.Arena.NewNumberInt(dpRank))
			}
		}
	}
	obj.Set("bootstrap_room", req.Arena.NewNumberInt(bootstrapRoom))
	obj.Set("bootstrap_host", req.Arena.NewString(req.Token.Endpoint.IP))
	obj.Set("rid", req.Arena.NewString(req.Id))

	req.Data = obj.MarshalTo(nil)
	klog.V(3).Infof("%v modifed pd proxy request %v", req.Id, string(req.Data))

	if obj.GetBool("stream") {
		hs.asyncHandleProxyPDStream(req)
	} else {
		hs.asyncHandleProxyPDNoStream(req)
	}
}

func (hs *LlmGatewayService) asyncHandleProxyPDNoStream(req *structs.Request) {

	var (
		prefillData *fastjson.Value
		decodeData  *fastjson.Value
		response    *http.Response
		eg          errgroup.Group
	)
	returnLogprob := req.ReqObj.GetBool("return_logprob")
	eg.Go(func() error {
		defer hs.lb.ReleaseToken(req, req.Token)

		newReq, err := hs.makeNewHttpRequest(req, req.Data, &req.Token.Endpoint, nil)
		if err != nil {
			return fmt.Errorf("make prefill request failed: %v", err)
		}
		resp, err := hs.client.Do(newReq)
		if err != nil {
			return fmt.Errorf("send prefill request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("prefill request failed: %v", resp.Status)
		}
		if returnLogprob {
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("read prefill response failed: %v", err)
			}
			pData, err := fastjson.ParseBytes(data)
			if err != nil {
				return fmt.Errorf("parse prefill response failed: %v", err)
			}
			klog.V(3).Infof("%v prefill response: %v", req.Id, string(pData.String()))
			prefillData = pData
		}
		return nil
	})

	eg.Go(func() error {
		defer hs.lb.ReleaseToken(req, req.SecondToken)

		newReq, err := hs.makeNewHttpRequest(req, req.Data, &req.SecondToken.Endpoint, nil)
		if err != nil {
			return fmt.Errorf("make decode request failed: %v", err)
		}
		resp, err := hs.client.Do(newReq)
		if err != nil {
			return fmt.Errorf("send decode request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("decode request failed: %v", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read decode response failed: %v", err)
		}
		dData, err := fastjson.ParseBytes(data)
		if err != nil {
			return fmt.Errorf("parse decode response failed: %v", err)
		}
		klog.V(3).Infof("%v decode response: %v", req.Id, string(dData.String()))
		decodeData = dData
		response = resp
		return nil
	})

	if err := eg.Wait(); err != nil {
		errMsg := err.Error()
		klog.Warningf("%v proxy pd request failed: %v", errMsg)

		// not retrun detail error
		if strings.Contains(errMsg, ":") {
			errMsg = errMsg[:strings.LastIndex(errMsg, ":")]
		}
		if response != nil {
			hs.doHttpErrorResponse(req, response.StatusCode, errMsg)
		} else {
			hs.doHttpErrorResponse(req, http.StatusInternalServerError, errMsg)
		}
		return
	}

	// try to merge logprobs
	if returnLogprob {
		utils.MergeTokenLogprobs(decodeData, prefillData, req.Arena)
	}

	if err := hs.handleResponse(req, response, decodeData.MarshalTo(nil)); err != nil {
		hs.doHttpErrorResponse(req, http.StatusInternalServerError, err.Error())
		return
	}

	hs.LogRequestAccess(req)
}

// TODO try to refactor handleDecodeStreamResponse
func (hs *LlmGatewayService) asyncHandleProxyPDStream(req *structs.Request) {

	var (
		decodeResp  *http.Response
		prefillResp *http.Response
		eg          errgroup.Group
	)
	eg.Go(func() error {

		newReq, err := hs.makeNewHttpRequest(req, req.Data, &req.Token.Endpoint, nil)
		if err != nil {
			return fmt.Errorf("make prefill request failed: %v", err)
		}
		resp, err := hs.client.Do(newReq)
		if err != nil {
			return fmt.Errorf("send prefill request failed: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("prefill request failed: %v", resp.Status)
		}
		prefillResp = resp
		return nil
	})

	eg.Go(func() error {

		newReq, err := hs.makeNewHttpRequest(req, req.Data, &req.SecondToken.Endpoint, nil)
		if err != nil {
			return fmt.Errorf("make decode request failed: %v", err)
		}
		resp, err := hs.client.Do(newReq)
		if err != nil {
			return fmt.Errorf("send decode request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("decode request failed: %v", resp.StatusCode)
		}
		decodeResp = resp
		return nil
	})

	if err := eg.Wait(); err != nil {
		errMsg := err.Error()
		klog.Warningf("%v proxy pd stream failed: %v", req.Id, errMsg)
		if strings.Contains(errMsg, ":") {
			errMsg = errMsg[:strings.LastIndex(errMsg, ":")]
		}
		hs.doHttpErrorResponse(req, http.StatusInternalServerError, errMsg)
		return
	}
	defer func() {
		decodeResp.Body.Close()
		prefillResp.Body.Close()
	}()

	// release decode token after return, prefill token in handleDecodeStreamResponse
	defer hs.lb.ReleaseToken(req, req.SecondToken)

	if err := hs.handleDecodeStreamResponse(req, decodeResp.Body, req.SecondToken, false, consts.NormalInferMode); err != nil {
		hs.doHttpErrorResponse(req, http.StatusInternalServerError, err.Error())
		return
	}

	hs.LogRequestAccess(req)
}

func (hs *LlmGatewayService) doResponseHeader(req *structs.Request, statusCode int, respHeaders http.Header) {
	if req.HeaderResponded {
		return
	}
	writer := req.Writer
	utils.DelHopHeaders(respHeaders)
	utils.CopyHeader(writer.Header(), respHeaders)
	writer.WriteHeader(statusCode)
	req.StatusCode = statusCode
	req.HeaderResponded = true
}

func (hs *LlmGatewayService) handleResponse(req *structs.Request, resp *http.Response, decodeData []byte) error {
	writer := req.Writer
	resp.Header.Del("content-length")
	resp.ContentLength = int64(len(decodeData))
	utils.DelHopHeaders(resp.Header)
	utils.CopyHeader(writer.Header(), resp.Header)
	writer.WriteHeader(resp.StatusCode)
	req.HeaderResponded = true
	_, err := writer.Write(decodeData)
	return err
}

func (hs *LlmGatewayService) BuildVllmKvtBody(req *structs.Request) {
	if len(req.Data) == 0 {
		return
	}
	jq, err := jsquery.NewBytesQuery(req.Data)
	if err != nil {
		klog.Errorf("request body format error: [%s]", string(req.Data))
		return
	}

	if jq.QueryToStringWithDefault("request_id", "") == "" {
		jq.Set("request_id", req.Id)
	}

	kv_transfer_params := map[string]interface{}{
		"remote_host": req.Token.Endpoint.IP,
		"remote_port": req.Token.Endpoint.OptionPort,
	}
	jq.Set("kv_transfer_params", kv_transfer_params)
	req.Data = jq.Bytes()
}

func (hs *LlmGatewayService) BuildVllmV6dBody(req *structs.Request) {
	if len(req.Data) == 0 {
		return
	}

	jq, err := jsquery.NewBytesQuery(req.Data)
	if err != nil {
		klog.Errorf("request body format error: [%s]", string(req.Data))
		return
	}

	jq.Set("request_id", req.Id)
	pV6dAddress := fmt.Sprintf("%s:%d", req.Token.Endpoint.IP, req.Token.Endpoint.OptionPort)
	jq.Set("prefill_v6d_address", pV6dAddress)
	if !hs.config.SeparatePDSchedule {
		prefillURL := fmt.Sprintf("http://%s:%d%s", req.Token.Endpoint.IP, req.Token.Endpoint.Port, req.Req.URL.String())
		jq.Set("prefill_endpoint", prefillURL)
	}
	req.Data = jq.Bytes()
}

func (hs *LlmGatewayService) BuildVllmMooncakePrefillBody(req *structs.Request) {
	if len(req.Data) == 0 {
		return
	}

	jq, err := jsquery.NewBytesQuery(req.Data)
	if err != nil {
		klog.Errorf("request body format error: [%s]", string(req.Data))
		return
	}

	jq.Set("stream", false)
	jq.Set("max_tokens", 1)
	if jq.QueryToStringWithDefault("max_completion_tokens", "") != "" {
		jq.Set("max_completion_tokens", 1)
	}
	if jq.QueryToStringWithDefault("stream_options", "") != "" {
		jq.Remove("stream_options")
	}

	// Set kv_transfer_params
	kvTransferParams := map[string]interface{}{
		"do_remote_decode":  true,
		"do_remote_prefill": false,
		"remote_engine_id":  nil,
		"remote_block_ids":  nil,
		"remote_host":       nil,
		"remote_port":       nil,
	}
	jq.Set("kv_transfer_params", kvTransferParams)

	req.Data = jq.Bytes()
}

func (hs *LlmGatewayService) BuildVllmMooncakeDecodeBody(req *structs.Request) {
	if len(req.Data) == 0 {
		return
	}

	jq, err := jsquery.NewBytesQuery(req.RawData)
	if err != nil {
		klog.Errorf("request body format error: [%s]", string(req.RawData))
		return
	}

	// Set kv_transfer_params from prefill response
	jq.Set("kv_transfer_params", req.KVTransferParams)

	req.Data = jq.Bytes()
	klog.V(3).Infof("request body: %s", string(req.Data))
}

func (hs *LlmGatewayService) SetRequestID(req *structs.Request) {
	if len(req.Data) == 0 {
		return
	}
	jq, err := jsquery.NewBytesQuery(req.Data)
	if err != nil {
		klog.Errorf("request body format error: [%s]", string(req.Data))
		return
	}

	if jq.QueryToStringWithDefault("request_id", "") == "" {
		jq.Set("request_id", req.Id)
	}
	req.Data = jq.Bytes()
}

func (hs *LlmGatewayService) handleHttpWithNormalInferMode(req *structs.Request) {
	if req.Token.InferMode != consts.NormalInferMode && hs.config.IsPDSplitMode() && req.SecondToken == nil {
		panic("split mode config may be not right.")
	}

	defer hs.lb.ReleaseToken(req, req.Token)
	if req.SecondToken != nil {
		defer hs.lb.ReleaseToken(req, req.SecondToken)
	}

	// detect if the client has disconnected
	if !utils.IsRequestConnected(req.Req) {
		klog.Warningf("[%s] client disconnected before processing, error: %v", req.Id, req.Req.Context().Err())
		req.StatusCode = consts.StatusDisConnection
		hs.LogRequestAccess(req)
		return
	}

	targetToken := req.Token
	if hs.config.IsVllmKvtSplitMode() {
		targetToken = req.SecondToken
		hs.BuildVllmKvtBody(req)
	} else if hs.config.IsVllmV6dSplitMode() {
		targetToken = req.SecondToken
		hs.BuildVllmV6dBody(req)
	} else if hs.config.IsVllmMooncakeSplitMode() {
		// already finish prefill, process decoding
		targetToken = req.SecondToken
		hs.BuildVllmMooncakeDecodeBody(req)
	}

	if hs.config.LlumnixConfig.EnableFullModeScheduling {
		hs.SetRequestID(req)
	}

	newFunc := func() (*http.Request, error) {
		// create normal request
		newReq, err := hs.makeNewHttpRequest(req, req.Data, &targetToken.Endpoint, nil)
		if err != nil {
			klog.Warningf("[%s] make new http request to backend failed: %v", req.Id, err)
			return nil, err
		}
		return newReq, nil
	}

	// do http request
	// http.Client.Do will be blocked until the first packet is received, so for convenience, we record this request here.
	hs.record(req.Token.Endpoint, req.Model)
	resp, newReq, err := hs.doHttpRequest(newFunc)
	if err != nil {
		hs.erase(req.Token.Endpoint, req.Model)
		hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
		return
	}
	hs.erase(req.Token.Endpoint, req.Model)
	defer resp.Body.Close()

	if !req.HeaderResponded && resp.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(resp.Body)
		klog.Infof("[%s] response status code: %d, %s", req.Id, resp.StatusCode, string(text))
		hs.doHttpErrorResponse(req, resp.StatusCode, string(text))
		return
	}
	// Store only the header information, not the entire response
	// This avoids holding references to the response body which will be closed
	req.ResponseHeader = resp.Header.Clone()
	req.StatusCode = resp.StatusCode

	// streaming copy
	needRecord := utils.IsNeedRecordedRequest(req.Token.InferBackend, newReq.URL.String())
	// handle decode request
	err = hs.handleDecodeStreamResponse(req, resp.Body, targetToken, needRecord, consts.NormalInferMode)
	if err == nil {
		hs.LogRequestAccess(req)
		return
	}

	klog.Errorf("[%s] handle request failed: %v", req.Id, err)
	hs.doHttpErrorResponse(req, http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
}

func (hs *LlmGatewayService) handleHttpWithSeparatePDScheduleMode(req *structs.Request) {

	err := hs.handlePrefillRequest(req, true)
	if err != nil {
		return
	}

	// select decode instance
	req.ScheduleStage = consts.DecodeInferMode
	nextTokens, err := hs.lb.GetNextTokens(req)
	if err != nil {
		klog.Warningf("[%s] could not get a decode backend endpoint: %v", req.Id, err)
		req.StatusCode = http.StatusBadRequest
		hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
		return
	}
	req.SecondToken = &nextTokens.Tokens[0]
	req.DecodeBalanceTime = time.Now()

	klog.V(3).Infof("%s start do decode with %s", req.Id, req.SecondToken.Endpoint.String())
	// handle decode response
	hs.handleHttpWithNormalInferMode(req)
}

func (hs *LlmGatewayService) handleHttpRequest(req *structs.Request) {
	endpoint := req.Token.Endpoint
	newFunc := func() (*http.Request, error) {
		newReq, err := hs.makeNewHttpRequest(req, req.Data, &endpoint, nil)
		if err != nil {
			klog.Warningf("[%s] make new http request to backend failed: %v", req.Id, err)
			return nil, err
		}
		return newReq, nil
	}

	// do http request
	resp, _, err := hs.doHttpRequest(newFunc)
	if err != nil {
		klog.Warningf("[%s] do http request to backend service failed: %v", req.Id, err)
		hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
		return
	}
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		klog.Warningf("[%s] read response of backend service failed: %v", req.Id, err)
		hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
		return
	}
	defer resp.Body.Close()

	writer := req.Writer
	utils.DelHopHeaders(resp.Header)
	utils.CopyHeader(writer.Header(), resp.Header)
	writer.WriteHeader(resp.StatusCode)
	req.HeaderResponded = true
	_, err = writer.Write(bytes)
	if err != nil {
		klog.Warningf("[%s] write response to client failed: %v", req.Id, err)
		hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
		return
	}
	req.StatusCode = resp.StatusCode
	hs.LogRequestAccess(req)
}

func (hs *LlmGatewayService) handleHttpToExternalService(req *structs.Request) {
	reqData := req.Data

	// using model in externalEp as request model
	if req.ExternalEp.Model != "" {
		var reqObj map[string]interface{}
		if err := json.Unmarshal(req.Data, &reqObj); err != nil {
			klog.Warningf("[%s] unmarshal request body failed: %v", req.Id, err)
			hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
			return
		}
		reqObj["model"] = req.ExternalEp.Model
		reqData, _ = json.Marshal(reqObj)
	}

	newFunc := func() (*http.Request, error) {
		newReq, err := hs.makeNewHttpRequest(req, reqData, nil, nil)
		if err != nil {
			klog.Warningf("[%s] make new http request to external failed: %v", req.Id, err)
			return nil, err
		}
		return newReq, nil
	}

	// do http request
	resp, _, err := hs.doHttpRequest(newFunc)
	if err != nil {
		klog.Warningf("[%s] do http request to external service failed: %v", req.Id, err)
		hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
		return
	}

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		klog.Warningf("[%s] read response of external service failed: %v", req.Id, err)
		hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		klog.Warningf("[%s] bad status code from external service: %d", req.Id, resp.StatusCode)
		hs.doHttpErrorResponse(req, resp.StatusCode, "http request to external service failed")
		return
	}

	writer := req.Writer
	utils.DelHopHeaders(resp.Header)
	utils.CopyHeader(writer.Header(), resp.Header)
	writer.WriteHeader(resp.StatusCode)
	req.HeaderResponded = true
	_, err = writer.Write(bytes)
	if err != nil {
		klog.Warningf("[%s] write response to client failed: %v", req.Id, err)
		hs.doHttpErrorResponse(req, http.StatusBadRequest, err.Error())
		return
	}
	req.StatusCode = resp.StatusCode
	hs.LogRequestAccess(req)
}

func (hs *LlmGatewayService) handleHttp(req *structs.Request) {
	if req.ExternalEp != nil {
		hs.handleHttpToExternalService(req)
		return
	}
	if req.NoSchedule {
		hs.handleHttpRequest(req)
		return
	}
	if req.ScheduleStage == consts.PrefillInferMode {
		hs.handleHttpWithSeparatePDScheduleMode(req)
	} else {
		if hs.config.IsSGLangMooncakeSplitMode() {
			hs.asyncHandleProxyPD(req)
		} else if hs.config.IsVllmMooncakeSplitMode() {
			err := hs.handlePrefillRequest(req, true)
			if err != nil {
				return
			}
			hs.handleHttpWithNormalInferMode(req)
		} else {
			hs.handleHttpWithNormalInferMode(req)
		}
	}
}

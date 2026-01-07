package service

import (
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/metrics"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"
)

func bindCtrlMessage(wsInConn, wsOutConn *websocket.Conn) {
	wsInConn.SetPingHandler(func(appData string) error {
		return wsOutConn.WriteMessage(websocket.PingMessage, []byte(appData))
	})
	wsInConn.SetPongHandler(func(appData string) error {
		return wsOutConn.WriteMessage(websocket.PongMessage, []byte(appData))
	})
	wsInConn.SetCloseHandler(func(code int, text string) error {
		message := websocket.FormatCloseMessage(code, text)
		wsOutConn.WriteControl(websocket.CloseMessage, message, time.Now().Add(time.Second))
		return nil
	})
	wsOutConn.SetPingHandler(func(appData string) error {
		return wsInConn.WriteMessage(websocket.PingMessage, []byte(appData))
	})
	wsOutConn.SetPongHandler(func(appData string) error {
		return wsInConn.WriteMessage(websocket.PongMessage, []byte(appData))
	})
	wsOutConn.SetCloseHandler(func(code int, text string) error {
		message := websocket.FormatCloseMessage(code, text)
		wsInConn.WriteControl(websocket.CloseMessage, message, time.Now().Add(time.Second))
		return nil
	})
}

type CopyError struct {
	wErr error
	rErr error
}

func startWsCopy(dst, src *websocket.Conn, fn func(int)) chan CopyError {
	errc := make(chan CopyError, 1)
	go func() {
		for {
			src.SetReadDeadline(time.Now().Add(5 * time.Minute))
			msgType, msg, err := src.ReadMessage()
			if err != nil {
				klog.V(3).Infof("Read Message error: %v", err)
				errc <- CopyError{nil, err}
				break
			}
			if fn != nil {
				fn(len(msg))
			}
			dst.SetWriteDeadline(time.Now().Add(5 * time.Second))
			err = dst.WriteMessage(msgType, msg)
			if err != nil {
				klog.V(3).Infof("Write Message error: %v", err)
				errc <- CopyError{err, nil}
				break
			}
		}
		klog.V(3).Infof("stream copy closed ....")
	}()
	return errc
}

func (hs *LlmGatewayService) handleWebsocket(r *structs.Request) {
	defer hs.lb.ReleaseToken(r, r.Token)

	req := r.Req
	w := r.Writer
	requestHeader := http.Header{}
	utils.CopyHeader(requestHeader, req.Header)
	// remove Hop-by-hop headers and websocket limited header
	utils.DelHopHeaders(requestHeader)
	requestHeader.Del("Sec-Websocket-Key")
	requestHeader.Del("Sec-Websocket-Version")
	// add x-forward-header
	clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
	if err == nil {
		utils.AppendHostToXForwardHeader(requestHeader, clientIP)
		r.DownStream = clientIP
	}
	requestHeader.Add("x-proxy-server", "llm-gateway")

	var wsOutConn *websocket.Conn
	var resp *http.Response
	// Connect to the backend endpoint, also pass the headers we get from the request
	dialer := websocket.DefaultDialer
	wsURL := fmt.Sprintf("ws://%s%s", r.Token.Endpoint.String(), req.URL.String())
	wsOutConn, resp, err = dialer.Dial(wsURL, requestHeader)
	if err != nil {
		klog.Warningf("dial websocket to %s: %v, retry cnt: %v", wsURL, err, r.ReScheduleCount)
		if resp != nil {
			utils.CopyResponse(w, resp)
			r.StatusCode = resp.StatusCode
			hs.LogRequestAccess(r)
		} else {
			if r.ReScheduleCount < hs.config.RetryCount {
				r.ReScheduleCount += 1
				hs.tryNext(r)
			} else {
				http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
				r.StatusCode = http.StatusServiceUnavailable
				hs.LogRequestAccess(r)
			}
		}
		return
	}
	requestsLabel := r.DefaultLabels.Append(structs.Label{Name: "status_code", Value: strconv.Itoa(resp.StatusCode)})
	metrics.Counter("llm_requests", requestsLabel).IncrByOne()
	r.StatusCode = websocket.CloseNormalClosure

	defer wsOutConn.Close()

	hs.record(r.Token.Endpoint, r.Model)

	// copy response headers to the upgrader.
	upgradeHeader := resp.Header
	utils.DelHopHeaders(upgradeHeader)
	upgradeHeader.Del("Sec-Websocket-Accept")

	// upgrade the existing incoming request to a WebSocket connection.
	wsInConn, err := hs.upgrader.Upgrade(w, req, upgradeHeader)
	if err != nil {
		klog.Warningf("couldn't upgrade %s", err)
		hs.erase(r.Token.Endpoint, r.Model)
		return
	}
	defer wsInConn.Close()

	// setup callback and forward ping pong and close frame
	bindCtrlMessage(wsInConn, wsOutConn)

	// transfer websocket data
	first := true
	bErased := false
	var lastCopyTime time.Time
	errIn := startWsCopy(wsInConn, wsOutConn, func(n int) {
		if first {
			hs.erase(r.Token.Endpoint, r.Model)
			bErased = true
			r.FirstTime = time.Now()
			lastCopyTime = r.FirstTime
			firstCost := r.FirstTime.Sub(r.EnQueueTime)
			metrics.Latency("llm_ttft", r.DefaultLabels).Add(firstCost.Milliseconds())
			first = false
		} else {
			now := time.Now()
			timeOutput := now.Sub(lastCopyTime)
			r.Itls = append(r.Itls, timeOutput.Milliseconds())
			lastCopyTime = now
		}
	})
	errOut := startWsCopy(wsOutConn, wsInConn, nil)

	// wait a websocket conn to be closed
	connCheckAndWait(errIn, errOut, wsInConn, wsOutConn, r)

	if !bErased {
		hs.erase(r.Token.Endpoint, r.Model)
	}
	metrics.Latency("llm_tpot", r.DefaultLabels).AddMany(r.Itls)

	hs.LogRequestAccess(r)
}

func handleError(errCopy CopyError, in bool, r *structs.Request) error {
	forwardEndpoint := r.Token.Endpoint

	if errCopy.wErr != nil {
		err := errCopy.wErr
		if in {
			klog.Warningf("error write from server(%s) to client(%s): %v", forwardEndpoint.String(), r.Req.RemoteAddr, err)
		} else {
			klog.Warningf("error write from client(%s) to backend(%s): %v", r.Req.RemoteAddr, forwardEndpoint.String(), err)
		}
		r.StatusCode = consts.StatusDisConnection
		return errCopy.wErr
	}
	if errCopy.rErr != nil {
		e, ok := errCopy.rErr.(*websocket.CloseError)
		if ok && e.Code == websocket.CloseAbnormalClosure {
			if in {
				klog.Warningf("error read from server(%s) to client(%s): %v", forwardEndpoint.String(), r.Req.RemoteAddr, errCopy.rErr)
			} else {
				klog.Warningf("error read from client(%s) to backend(%s): %v", r.Req.RemoteAddr, forwardEndpoint.String(), errCopy.rErr)
			}
			r.StatusCode = websocket.CloseAbnormalClosure
			return errCopy.rErr
		}
	}
	return nil
}

func connCheckAndWait(inChan, outChan chan CopyError, connIn, ConnOut *websocket.Conn, r *structs.Request) error {
	var errCopy CopyError
	var err, errIn, errOut error
	CloseIn := false
	CloseOut := false

	select {
	case errCopy = <-inChan: // server -> client
		errIn = handleError(errCopy, true, r)
		CloseIn = true
	case errCopy = <-outChan: // client -> server
		errOut = handleError(errCopy, false, r)
		CloseOut = true
	}

	if CloseIn {
		if errIn != nil {
			err = errIn
			connIn.Close()
			ConnOut.Close()
		}
		<-outChan
	}
	if CloseOut {
		if errOut != nil {
			err = errOut
			connIn.Close()
			ConnOut.Close()
		}
		<-inChan
	}
	return err
}

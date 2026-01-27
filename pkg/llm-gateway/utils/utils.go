package utils

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/valyala/fastjson"
	"k8s.io/klog/v2"

	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/util/jsquery"
)

// Hop-by-hop headers. These are removed when sent to the backend.
// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te", // canonicalized version of "TE"
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
	"Content-Length",
}

func CopyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func CopyResponse(rw http.ResponseWriter, resp *http.Response) error {
	CopyHeader(rw.Header(), resp.Header)
	rw.WriteHeader(resp.StatusCode)
	defer resp.Body.Close()

	_, err := io.Copy(rw, resp.Body)
	return err
}

func DelHopHeaders(header http.Header) {
	for _, h := range hopHeaders {
		header.Del(h)
	}
}

func AppendHostToXForwardHeader(header http.Header, host string) {
	// If we aren't the first proxy retain prior
	// X-Forwarded-For information as a comma+space
	// separated list and fold multiple headers into one.
	if prior, ok := header["X-Forwarded-For"]; ok {
		host = strings.Join(prior, ", ") + ", " + host
	}
	header.Set("X-Forwarded-For", host)
}

// Token octets per RFC 2616.
var isTokenOctet = [256]bool{
	'!':  true,
	'#':  true,
	'$':  true,
	'%':  true,
	'&':  true,
	'\'': true,
	'*':  true,
	'+':  true,
	'-':  true,
	'.':  true,
	'0':  true,
	'1':  true,
	'2':  true,
	'3':  true,
	'4':  true,
	'5':  true,
	'6':  true,
	'7':  true,
	'8':  true,
	'9':  true,
	'A':  true,
	'B':  true,
	'C':  true,
	'D':  true,
	'E':  true,
	'F':  true,
	'G':  true,
	'H':  true,
	'I':  true,
	'J':  true,
	'K':  true,
	'L':  true,
	'M':  true,
	'N':  true,
	'O':  true,
	'P':  true,
	'Q':  true,
	'R':  true,
	'S':  true,
	'T':  true,
	'U':  true,
	'W':  true,
	'V':  true,
	'X':  true,
	'Y':  true,
	'Z':  true,
	'^':  true,
	'_':  true,
	'`':  true,
	'a':  true,
	'b':  true,
	'c':  true,
	'd':  true,
	'e':  true,
	'f':  true,
	'g':  true,
	'h':  true,
	'i':  true,
	'j':  true,
	'k':  true,
	'l':  true,
	'm':  true,
	'n':  true,
	'o':  true,
	'p':  true,
	'q':  true,
	'r':  true,
	's':  true,
	't':  true,
	'u':  true,
	'v':  true,
	'w':  true,
	'x':  true,
	'y':  true,
	'z':  true,
	'|':  true,
	'~':  true,
}

// skipSpace returns a slice of the string s with all leading RFC 2616 linear
// whitespace removed.
func skipSpace(s string) (rest string) {
	i := 0
	for ; i < len(s); i++ {
		if b := s[i]; b != ' ' && b != '\t' {
			break
		}
	}
	return s[i:]
}

// nextToken returns the leading RFC 2616 token of s and the string following
// the token.
func nextToken(s string) (token, rest string) {
	i := 0
	for ; i < len(s); i++ {
		if !isTokenOctet[s[i]] {
			break
		}
	}
	return s[:i], s[i:]
}

// equalASCIIFold returns true if s is equal to t with ASCII case folding as
// defined in RFC 4790.
func equalASCIIFold(s, t string) bool {
	for s != "" && t != "" {
		sr, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		tr, size := utf8.DecodeRuneInString(t)
		t = t[size:]
		if sr == tr {
			continue
		}
		if 'A' <= sr && sr <= 'Z' {
			sr = sr + 'a' - 'A'
		}
		if 'A' <= tr && tr <= 'Z' {
			tr = tr + 'a' - 'A'
		}
		if sr != tr {
			return false
		}
	}
	return s == t
}

// HeaderContainsValue returns true if the 1#token header with the given
// name contains a token equal to value with ASCII case folding.
func HeaderContainsValue(header http.Header, name string, value string) bool {
headers:
	for _, s := range header[name] {
		for {
			var t string
			t, s = nextToken(skipSpace(s))
			if t == "" {
				continue headers
			}
			s = skipSpace(s)
			if s != "" && s[0] != ',' {
				continue headers
			}
			if equalASCIIFold(t, value) {
				return true
			}
			if s == "" {
				continue headers
			}
			s = s[1:]
		}
	}
	return false
}

// IsRequestConnected checks if HTTP request is still connected/active.
// Note: The Context's Done signal can only be reliably captured after attempting a read or write operation.
func IsRequestConnected(request *http.Request) bool {
	select {
	case <-request.Context().Done():
		return false
	default:
		return true
	}
}

// extractContent takes a byte slice of data and a JSON path string,
// and returns the string content found at the specified path within the JSON data.
func extractContent(dataBytes []byte, path string) (string, error) {
	prefix := []byte("data:")
	newDataBytes := bytes.TrimSpace(dataBytes)
	if bytes.HasPrefix(newDataBytes, prefix) {
		newDataBytes = bytes.TrimPrefix(newDataBytes, prefix)
	} else {
		return "", nil
	}

	jq, err := jsquery.NewBytesQuery(newDataBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create jq: %v, data: %v, path: %v", err, string(dataBytes), path)
	}
	val, err := jq.Query(path)
	if err != nil {
		return "", nil
	}
	return val.(string), err
}

const (
	vllmBackend  = "vllm"
	bladeBackend = "blade"

	llmChatPath        = "/v1/chat/completions"
	llmCompletionsPath = "/v1/completions"
)

func IsNeedRecordedRequest(backend, reqPath string) bool {
	switch backend {
	case vllmBackend:
		return reqPath == llmCompletionsPath
	case bladeBackend:
		// TODO: support blade chat/completions
		return reqPath == llmCompletionsPath
	default:
		return false
	}
}

// mergeRequestWithContent takes the original HTTP request and the concatenated content string,
// and creates a new request body with the added assistant message.
func MergeLLMReqBodyWithContent(backend string, req *http.Request, bodyBytes []byte, recordedMsg *[][]byte) ([]byte, error) {
	var requestMessagePath, contentPath, maxTokensPath string

	switch req.URL.String() {
	case llmChatPath:
		if strings.ToLower(backend) != bladeBackend {
			return nil, fmt.Errorf("chat request only support blade, now backend: %s, url: %s", backend, req.URL.String())
		}
		requestMessagePath = "messages"
		contentPath = "choices.[0].delta.content"
	case llmCompletionsPath:
		requestMessagePath = "prompt"
		contentPath = "choices.[0].text"
		maxTokensPath = "max_tokens"
	default:
		return nil, fmt.Errorf("unsupported request to merge, url: %s", req.URL.String())
	}

	// Extract the specific content from the recordedMsg
	var tokensLen int
	var addContents strings.Builder
	for _, bytes := range *recordedMsg {
		if content, err := extractContent(bytes, contentPath); err != nil {
			klog.Warningf("failed to extract content from the contnet: %v", err)
		} else {
			addContents.WriteString(content)
			tokensLen++
		}
	}

	// Add the new assistant message with the concatenated content.
	jq, err := jsquery.NewBytesQuery(bodyBytes)
	if err != nil {
		return nil, err
	}
	switch req.URL.String() {
	case llmChatPath:
		messages, err := jq.QueryToArray(requestMessagePath)
		if err != nil {
			return nil, err
		}
		// Add the new assistant message with the concatenated content.
		messages = append(messages, map[string]string{
			"role":    "assistant",
			"content": addContents.String(),
		})
		err = jq.Set(requestMessagePath, messages)
		if err != nil {
			return nil, err
		}
	case llmCompletionsPath:
		prompt, err := jq.QueryToString(requestMessagePath)
		if err != nil {
			return nil, err
		}
		err = jq.Set(requestMessagePath, prompt+addContents.String())
		if err != nil {
			return nil, err
		}
		if jq.Has(maxTokensPath) {
			// Update the max_tokens, subtract the tokens already generated by the current request
			if maxTokens, err := jq.QueryToInt(maxTokensPath); err == nil {
				if tokensLen > maxTokens {
					err = fmt.Errorf("recorded tokensLen: %d is out of maxTokens: %d", tokensLen, maxTokens)
				} else {
					err = jq.Set(maxTokensPath, maxTokens-tokensLen)
				}
			}
			if err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unsupported request to merge, url: %s", req.URL.String())
	}

	// Update the request
	jqBytes := jq.Bytes()
	req.ContentLength = int64(len(jqBytes))
	req.Header.Set("Content-Length", strconv.FormatInt(req.ContentLength, 10))

	return jqBytes, nil
}

func MergeTokenLogprobs(decodeData, prefillData *fastjson.Value, arena fastjson.Arena) {
	if decodeData == nil || prefillData == nil {
		return
	}
	if metaInfo := decodeData.Get("meta_info"); metaInfo != nil {
		if inputTokenLogprobs := metaInfo.Get("input_token_logprobs"); inputTokenLogprobs != nil {
			if prefillMetaInfo := prefillData.Get("meta_info"); prefillMetaInfo != nil {
				if prefillInputTokenLogprobs := prefillMetaInfo.Get("input_token_logprobs"); prefillInputTokenLogprobs != nil {
					prefillArray := prefillInputTokenLogprobs.GetArray()
					decodeArray := inputTokenLogprobs.GetArray()
					mergedArray := arena.NewArray()
					idx := 0
					for _, e := range prefillArray {
						mergedArray.SetArrayItem(idx, e)
						idx++
					}
					for _, e := range decodeArray {
						mergedArray.SetArrayItem(idx, e)
						idx++
					}
					metaInfo.Set("input_token_logprobs", mergedArray)
					decodeData.Set("meta_info", metaInfo)
				}
			}
		}
	}
}

func LogAccess(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), msg)
}

type SimpleLogPacket struct {
	msg string
}

type SimpleAsyncLogger struct {
	logChan   chan *SimpleLogPacket // logger buffer
	closeChan chan struct{}         // close signal
}

func NewSimpleAsyncLogger() *SimpleAsyncLogger {
	l := &SimpleAsyncLogger{
		logChan:   make(chan *SimpleLogPacket, 1000),
		closeChan: make(chan struct{}),
	}
	go l.processLogLoop()
	return l
}

func (l *SimpleAsyncLogger) Close() {
	close(l.closeChan)
}

func (l *SimpleAsyncLogger) Log(msg string) {
	select {
	case l.logChan <- &SimpleLogPacket{msg: msg}:
	default:
		// drop the log if the buffer is full
	}
}

func (l *SimpleAsyncLogger) processLogLoop() {
	for {
		select {
		case p := <-l.logChan:
			fmt.Printf("%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), p.msg)
		case <-l.closeChan:
			return
		}
	}
}

var (
	asyncLogger *SimpleAsyncLogger
	once        sync.Once
)

func AsyncLog(format string, args ...any) {
	once.Do(func() {
		asyncLogger = NewSimpleAsyncLogger()
	})
	msg := fmt.Sprintf(format, args...)
	asyncLogger.Log(msg)
}

func Md5sum(data []byte) string {
	return fmt.Sprintf("%x", md5.Sum(data))
}

var instanceType2InferModeMap = map[string]string{
	consts.LlumnixNeutralInstanceType: consts.NormalInferMode,
	consts.LlumnixPrefillInstanceType: consts.PrefillInferMode,
	consts.LlumnixDecodeInstanceType:  consts.DecodeInferMode,
}

func TransformInstanceType2InferMode(instanceType string) string {
	if mode, exists := instanceType2InferModeMap[instanceType]; exists {
		return mode
	}
	klog.Warningf("Unknown instance type: %s", instanceType)
	return ""
}

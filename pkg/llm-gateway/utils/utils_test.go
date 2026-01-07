package utils

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/valyala/fastjson"
	"gotest.tools/assert"
	"k8s.io/klog/v2"
)

func TestExtractContent(t *testing.T) {
	data := `
data: {
	"id": "cmpl-1cd19d4ff98447779e46df7a3ce18500",
	"object": "chat.completion.chunk",
	"created": 12935,
	"model": "llama2",
	"choices": [{
		"index": 0,
		"delta": {
			"content": " Hello"
		},
		"logprobs": null,
		"finish_reason": null
	}]
}
`
	content, err := extractContent([]byte(data), "choices.[0].delta.content")
	assert.Check(t, err)
	assert.Equal(t, content, " Hello")
}

func TestMergeChatRequest(t *testing.T) {
	msgStrSlice := []string{
		`data:{"id":"cmpl-1cd19d4ff98447779e46df7a3ce18500","object":"chat.completion.chunk","created":12935,"model":"llama2","choices":[{"index":0,"delta":{"content":" "},"logprobs":null,"finish_reason":null}]}`,
		`data:{"id":"cmpl-1cd19d4ff98447779e46df7a3ce18500","object":"chat.completion.chunk","created":12935,"model":"llama2","choices":[{"index":0,"delta":{"content":" Hello"},"logprobs":null,"finish_reason":null}]}`,
		`data:{"id":"cmpl-1cd19d4ff98447779e46df7a3ce18500","object":"chat.completion.chunk","created":12935,"model":"llama2","choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}]}`,
	}
	var recordedMsg [][]byte
	for _, msg := range msgStrSlice {
		recordedMsg = append(recordedMsg, []byte(msg))
	}

	requestData := `
{
	"model": "gpt-3.5-turbo",
	"messages": [{
			"role": "system",
			"content": "You are a helpful assistant."
		},
		{
			"role": "user",
			"content": "Hello!"
		}
	]
}
`
	jsonBuffer := bytes.NewBuffer([]byte(requestData))
	req, err := http.NewRequest("POST", "/v1/chat/completions", jsonBuffer)
	if err != nil {
		fmt.Printf("Error: %s", err.Error())
		return
	}

	_, err = MergeLLMReqBodyWithContent("blade", req, []byte(requestData), &recordedMsg)
	if err != nil {
		t.Error(err)
	}
	fmt.Printf("%v\n", req.Body)
}

func TestMergeCompletionsRequest(t *testing.T) {
	msgStrSlice := []string{
		`data:{"id":"cmpl-7iA7iJjj8V2zOkCGvWF2hAkDWBQZe","object":"text_completion","created":1690759702,"choices":[{"text":"This","index":0,"logprobs":null,"finish_reason":null}],"model":"gpt-3.5-turbo-instruct","system_fingerprint":"fp_44709d6fcb"}`,
		`data:{"id":"cmpl-7iA7iJjj8V2zOkCGvWF2hAkDWBQZe","object":"text_completion","created":1690759702,"choices":[{"text":" ","index":0,"logprobs":null,"finish_reason":null}],"model":"gpt-3.5-turbo-instruct","system_fingerprint":"fp_44709d6fcb"}`,
		`data:{"id":"cmpl-7iA7iJjj8V2zOkCGvWF2hAkDWBQZe","object":"text_completion","created":1690759702,"choices":[{"text":"is","index":0,"logprobs":null,"finish_reason":null}],"model":"gpt-3.5-turbo-instruct","system_fingerprint":"fp_44709d6fcb"}`,
	}
	var recordedMsg [][]byte
	for _, msg := range msgStrSlice {
		recordedMsg = append(recordedMsg, []byte(msg))
	}

	requestData := `
	{
	   "model": "gpt-3.5-turbo-instruct",
	   "prompt": "Say this is a test",
	   "max_tokens": 7,
	   "temperature": 0,
	   "stream": true
	}
	`
	jsonBuffer := bytes.NewBuffer([]byte(requestData))
	req, err := http.NewRequest("POST", "/v1/completions", jsonBuffer)
	if err != nil {
		fmt.Printf("Error: %s", err.Error())
		return
	}

	_, err = MergeLLMReqBodyWithContent("blade", req, []byte(requestData), &recordedMsg)
	if err != nil {
		t.Error(err)
	}
	fmt.Printf("%v\n", req.Body)
}

// A concrete work item type
type LogWork struct {
	message string
}

// Process implements the Workable interface.
func (lw *LogWork) QueueProcess() {
	fmt.Println(lw.message)
	return
}

// TestPriorityQueue ensures that priority items are dequeued before regular items.
func TestPriority(t *testing.T) {
	pc := NewProxyQueue("", 5)

	// Create and enqueue some work items
	priorityWork := &LogWork{message: "priority work"}
	normalWork := &LogWork{message: "normal work"}
	pc.Enqueue(normalWork)
	pc.EnqueuePriority(priorityWork)

	// Verify that the priority item is dequeued first
	dequeuedWork := pc.WaitDequeue().(*LogWork)
	if message := dequeuedWork.message; message != "priority work" {
		t.Errorf("Expected priority work to be dequeued first, got: %s", message)
	}

	// Verify that the normal item is dequeued second
	dequeuedWork = pc.WaitDequeue().(*LogWork)
	if message := dequeuedWork.message; message != "normal work" {
		t.Errorf("Expected normal work to be dequeued second, got: %s", message)
	}
}

func TestMergeTokenLogprobs(t *testing.T) {
	prefillData := `{
		"meta_info": {
			"input_token_logprobs": [0.1, 0.2, 0.3]
		}
	}`

	decodeData := `{
		"meta_info": {
			"input_token_logprobs": [0.4, 0.5, 0.6]
		}
	}`

	prefillVal, _ := fastjson.Parse(prefillData)
	decodeVal, _ := fastjson.Parse(decodeData)

	// 创建一个 Arena 实例
	var arena fastjson.Arena
	MergeTokenLogprobs(decodeVal, prefillVal, arena)

	klog.Infof("decodeData: %s", decodeVal.String())
}

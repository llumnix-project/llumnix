package request_processor

import (
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// !important!: run make llm-gateway-build and add -ldflags="-extldflags '-L/tmp'" for your ide to run the test

type TokenizerTestSuite struct {
	suite.Suite
	tempDir       string
	deepSeekDir   string
	tokenizerPath string
	configPath    string
}

// SetupSuite run before all test cases
func (suite *TokenizerTestSuite) SetupSuite() {
	suite.tempDir = suite.T().TempDir()

	suite.deepSeekDir = filepath.Join(suite.tempDir, "DeepSeek-R1")
	err := os.MkdirAll(suite.deepSeekDir, 0755)
	require.NoError(suite.T(), err)

	tokenizerURL := "https://pai-quickstart-cn-shanghai.oss-cn-shanghai.aliyuncs.com/modelscope/deepseek/DeepSeek-R1-0528/tokenizer.json"
	suite.tokenizerPath = filepath.Join(suite.deepSeekDir, "tokenizer.json")

	err = downloadFile(tokenizerURL, suite.tokenizerPath)
	require.NoError(suite.T(), err)

	_, err = os.Stat(suite.tokenizerPath)
	assert.NoError(suite.T(), err, "tokenizer.json should exist")

	configURL := "https://pai-quickstart-cn-shanghai.oss-cn-shanghai.aliyuncs.com/modelscope/deepseek/DeepSeek-R1-0528/tokenizer_config.json"
	suite.configPath = filepath.Join(suite.deepSeekDir, "tokenizer_config.json")

	err = downloadFile(configURL, suite.configPath)
	require.NoError(suite.T(), err)

	_, err = os.Stat(suite.configPath)
	assert.NoError(suite.T(), err, "tokenizer_config.json should exist")
}

func (suite *TokenizerTestSuite) TestNewTokenizer() {
	tokenizer, err := newTokenizer("", "")
	assert.NoError(suite.T(), err)
	assert.Nil(suite.T(), tokenizer)

	tokenizer, err = newTokenizer("", suite.deepSeekDir)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), tokenizer)
}

// TestRequestProcessor_Encode test Encode function
func (suite *TokenizerTestSuite) TestRequestProcessor_Encode() {
	tokenizer, err := newTokenizer("", suite.deepSeekDir)
	require.NoError(suite.T(), err)

	processor := &RequestProcessor{
		tokenizer: tokenizer,
	}

	result, err := processor.encode("test text")
	assert.NoError(suite.T(), err)
	fmt.Printf("result: %v", result)
	assert.NotEmpty(suite.T(), result)
}

// TestRequestProcessor_ApplyChatTemplate test ApplyChatTemplate function
func (suite *TokenizerTestSuite) TestRequestProcessor_ApplyChatTemplate() {
	processor, err := NewRequestProcessor("", suite.deepSeekDir)
	require.NoError(suite.T(), err)

	messages := `[{"role": "system", "content": "You are a helpful assistant."},
        {
            "role": "user",
            "content": "你好吗"
        },
		{
			"role": "assistant",
			"content": "你好，有什么我可以帮助你的吗？"
		},
		{
			"role": "user",
			"content": "你能做什么？"
		}
    ]`

	result, err := processor.applyChatTemplate(messages)
	assert.NoError(suite.T(), err)
	fmt.Printf("apply chat template result: %v", result)
	assert.NotEmpty(suite.T(), result)
	assert.Equal(suite.T(), result, "<｜begin▁of▁sentence｜>You are a helpful assistant.<｜User｜>你好吗<｜Assistant｜>你好，有什么我可以帮助你的吗？<｜end▁of▁sentence｜><｜User｜>你能做什么？<｜Assistant｜>")
}

// TestRequestProcessor_ApplyChatTemplateAndEncode test ApplyChatTemplateAndEncode function
func (suite *TokenizerTestSuite) TestRequestProcessor_ApplyChatTemplateAndEncode() {
	processor, err := NewRequestProcessor("", suite.deepSeekDir)
	require.NoError(suite.T(), err)

	messages := `[{"role": "system", "content": "You are a helpful assistant."},
        {
            "role": "user",
            "content": "你好吗"
        },
		{
			"role": "assistant",
			"content": "你好，有什么我可以帮助你的吗？"
		},
		{
			"role": "user",
			"content": "你能做什么？"
		}
    ]`

	result, err := processor.applyChatTemplateAndEncode(messages)
	fmt.Printf("apply chat template result: %v", result)
	assert.NoError(suite.T(), err)
	assert.NotEmpty(suite.T(), result)

	assert.Equal(suite.T(), result, []uint32{0, 3476, 477, 260, 11502, 22896, 16, 128803, 30594, 3467, 128804, 30594, 303, 10457, 34071, 6399, 4597, 3467, 1148, 1, 128803, 24910, 24009, 1148, 128804})
}

// TestRequestProcessor_PostProcessCompletionsToChat test PostProcessCompletionsToChat function
func (suite *TokenizerTestSuite) TestRequestProcessor_PostProcessCompletionsToChat() {
	processor := &RequestProcessor{
		tokenizer: &mockTokenizer{},
	}

	// 模拟 completions 格式的响应
	completionResp := `{
	  "id": "cmpl-0918d40aa82942c897c2809e57f4c487",
	  "object": "text_completion",
	  "created": 1753869490,
	  "model": "Qwen/Qwen3-0.6B",
	  "choices": [
		{
		  "index": 0,
		  "text": "中的量子态与波函数的关系是什么？",
		  "logprobs": null,
		  "finish_reason": null,
		  "stop_reason": null,
		  "prompt_logprobs": null
		}
	  ],
	  "usage": {
		"prompt_tokens": 2,
		"total_tokens": 102,
		"completion_tokens": 100,
		"prompt_tokens_details": null
	  },
	  "kv_transfer_params": null
	}`
	req := &structs.Request{
		ReasoningParser:    utils.NewDefaultReasoningParser(),
		InputTokensLen:     0,
		ReasoningTokensLen: 0,
		OutputTokensLen:    0,
	}
	result := processor.PostProcess(req, []byte(completionResp))
	var resultMap map[string]interface{}
	err := json.Unmarshal(result, &resultMap)
	assert.NoError(suite.T(), err)

	assert.Equal(suite.T(), "chat.completion", resultMap["object"])
	assert.Equal(suite.T(), "chatcmpl-0918d40aa82942c897c2809e57f4c487", resultMap["id"])

	choices := resultMap["choices"].([]interface{})
	choice := choices[0].(map[string]interface{})
	message := choice["message"].(map[string]interface{})

	fmt.Printf("convert to chat response result: %v", string(result))
	assert.Equal(suite.T(), "assistant", message["role"])
	assert.Equal(suite.T(), "中的量子态与波函数的关系是什么？", message["content"])
}

// TestRequestProcessor_PostProcessStreamCompletionsToChat test PostProcessStreamCompletionsToChat function
func (suite *TokenizerTestSuite) TestRequestProcessor_PostProcessStreamCompletionsToChat() {
	processor := &RequestProcessor{
		tokenizer: &mockTokenizer{},
	}

	streamResp := `{
	  "id": "cmpl-8a529a47b57f4ebaa052dff83982eaad",
	  "object": "text_completion",
	  "created": 1753869450,
	  "model": "Qwen/Qwen3-0.6B",
	  "choices": [
		{
		  "index": 0,
		  "text": "函数",
		  "logprobs": null,
		  "finish_reason": "length",
		  "stop_reason": null
		}
	  ],
	  "usage": null
	}`

	req := &structs.Request{
		ReasoningParser:    utils.NewDefaultReasoningParser(),
		InputTokensLen:     0,
		ReasoningTokensLen: 0,
		OutputTokensLen:    0,
	}
	result := processor.PostProcessStream(req, []byte(streamResp), true)
	resultStr := string(result)

	fmt.Printf("convert to chat response result: %v", resultStr)

	assert.Contains(suite.T(), resultStr, "data:")
	assert.Contains(suite.T(), resultStr, "chat.completion.chunk")
	assert.Contains(suite.T(), resultStr, "chatcmpl-8a529a47b57f4ebaa052dff83982eaad")
	assert.Contains(suite.T(), resultStr, "\"role\":\"assistant\"")
	assert.Contains(suite.T(), resultStr, "completion_tokens_details")
	assert.Contains(suite.T(), resultStr, "\"delta\":{\"content\":\"函数\",\"reasoning_content\":")
	assert.NotContains(suite.T(), resultStr, "stop_reason")
	assert.Contains(suite.T(), resultStr, "\"finish_reason\":\"length\"")

	// test [DONE]
	doneResp := "[DONE]"
	result = processor.PostProcessStream(req, []byte(doneResp), false)
	assert.Equal(suite.T(), "data: [DONE]\n\n", string(result))
}

// TestRequestProcessor_PostProcessStreamCompletionsToChat test PostProcessStreamCompletionsToChat function
func (suite *TokenizerTestSuite) TestRequestProcessor_PostProcessStreamCompletionsToChatStop() {
	processor := &RequestProcessor{
		tokenizer: &mockTokenizer{},
	}

	streamResp := `{
  "id": "ec5ce30e-6ab7-4bd8-af23-f95e0b2ae70d",
  "object": "text_completion",
  "created": 1755085628,
  "model": "/mnt/oss/DeepSeek-R1-0528/",
  "choices": [
    {
      "index": 0,
      "text": "** 🌟",
      "logprobs": null,
      "finish_reason": "stop",
      "stop_reason": 1
    }
  ],
  "usage": null
}`

	req := &structs.Request{
		ReasoningParser:    utils.NewDefaultReasoningParser(),
		InputTokensLen:     0,
		ReasoningTokensLen: 0,
		OutputTokensLen:    0,
	}
	result := processor.PostProcessStream(req, []byte(streamResp), true)
	resultStr := string(result)

	fmt.Printf("convert to chat response result: %v", resultStr)
	//
	assert.Contains(suite.T(), resultStr, "data:")
	assert.Contains(suite.T(), resultStr, "chat.completion.chunk")
	assert.Contains(suite.T(), resultStr, "4bd8-af23-f95e0b2ae70d")
	assert.Contains(suite.T(), resultStr, "\"role\":\"assistant\"")
	assert.Contains(suite.T(), resultStr, "completion_tokens_details")
	assert.Contains(suite.T(), resultStr, "\"content\":\"** 🌟\"")
	assert.Contains(suite.T(), resultStr, "\"reasoning_content\":")
	assert.NotContains(suite.T(), resultStr, "stop_reason")
	assert.Contains(suite.T(), resultStr, "\"finish_reason\":\"stop\"")

	// test [DONE]
	//doneResp := "[DONE]"
	//result = processor.PostProcessStream(req, []byte(doneResp), false)
	//assert.Equal(suite.T(), "data: [DONE]\n\n", string(result))
}

// TestRequestProcessor_PostProcessStreamCompletionsToChat_NonFirstChunk
func (suite *TokenizerTestSuite) TestRequestProcessor_PostProcessStreamCompletionsToChat_NonFirstChunk() {
	processor := &RequestProcessor{
		tokenizer: &mockTokenizer{},
	}

	streamResp := `{
	  "id": "cmpl-8a529a47b57f4ebaa052dff83982eaad",
	  "object": "text_completion",
	  "created": 1753869450,
	  "model": "Qwen/Qwen3-0.6B",
	  "choices": [
		{
		  "index": 0,
		  "text": "函数",
		  "logprobs": null,
		  "finish_reason": "stop",
		  "stop_reason": null
		}
	  ],
	  "usage": null
	}`

	req := &structs.Request{
		ReasoningParser:    utils.NewDefaultReasoningParser(),
		InputTokensLen:     0,
		ReasoningTokensLen: 0,
		OutputTokensLen:    0,
	}
	result := processor.PostProcessStream(req, []byte(streamResp), false)
	resultStr := string(result)

	fmt.Printf("convert to chat response result: %v", resultStr)

	// non first chunk does not contain blank delta content
	assert.NotContains(suite.T(), resultStr, "\"delta\":{\"role\":\"assistant\",\"content\":\"\"}")
	assert.Contains(suite.T(), resultStr, "\"delta\":{\"content\":\"函数\",\"reasoning_content\":")
	assert.Contains(suite.T(), resultStr, "completion_tokens_details")
	assert.Contains(suite.T(), resultStr, "finish_reason")
	assert.NotContains(suite.T(), resultStr, "stop_reason")
}

func (suite *TokenizerTestSuite) TestReasoningParser() {
	reasoningParser := utils.NewReasoningParser("", "")
	modelOutput := "<think>abcdefg</think>hijklmn"
	reasoningContent, content := reasoningParser.ExtractReasoningContent(modelOutput)
	assert.Equal(suite.T(), "abcdefg", reasoningContent)
	assert.Equal(suite.T(), "hijklmn", content)

	modelOutput = "abcdefg</think>hijklmn"
	reasoningContent, content = reasoningParser.ExtractReasoningContent(modelOutput)
	assert.Equal(suite.T(), "abcdefg", reasoningContent)
	assert.Equal(suite.T(), "hijklmn", content)

	modelOutput = "<think>abcdefg"
	reasoningContent, content = reasoningParser.ExtractReasoningContent(modelOutput)
	assert.Equal(suite.T(), "abcdefg", reasoningContent)
	assert.Equal(suite.T(), "", content)

	modelOutput = "abcdefg"
	reasoningContent, content = reasoningParser.ExtractReasoningContent(modelOutput)
	assert.Equal(suite.T(), "abcdefg", content)
	assert.Equal(suite.T(), "", reasoningContent)
}

func (suite *TokenizerTestSuite) TestReasoningParserStream() {
	reasoningParser := utils.NewReasoningParser("", "")

	streams := []string{"<th", "i", "nk", ">", "好", "的", "，", "用户", "问", "“你", "好", "吗”", "，", "我需", "要", "以友", "好和亲", "切的", "方", "式回", "应", "。首", "先，", "确", "认对", "方的", "问候", "是", "否正", "确，", "然后", "表", "达关", "心和帮", "助", "的态", "度", "。同", "时，", "保", "持自", "然，", "避免", "使用", "过", "于", "正", "式", "或生", "硬", "的语", "气。", "可以", "简", "单地", "说“", "你好", "！”", "然后", "询", "问对", "方", "的状", "况", "，比", "如“", "今天过", "得", "怎么", "样？", "”", "，这", "样", "既回", "应", "了", "问候", "，", "又保", "持", "了", "互动", "性。", "确保", "回", "复简", "洁，", "符", "合中", "文的", "表达", "习惯。", "</th", "in", "k>", "你", "好！", "今天过", "得", "怎么样", "？有", "什", "么可", "以", "帮", "你的", "吗？ 😊"}
	reasoningContent, content := "", ""
	for _, text := range streams {
		reasoning, normal := reasoningParser.ExtractReasoningContentStreaming(text)
		reasoningContent += reasoning
		content += normal
	}
	//fmt.Printf("reasoningContent: %s\n, content: %s\n", reasoningContent, content)
	assert.Equal(suite.T(), `好的，用户问“你好吗”，我需要以友好和亲切的方式回应。首先，确认对方的问候是否正确，然后表达关心和帮助的态度。同时，保持自然，避免使用过于正式或生硬的语气。可以简单地说“你好！”然后询问对方的状况，比如“今天过得怎么样？”，这样既回应了问候，又保持了互动性。确保回复简洁，符合中文的表达习惯。`, reasoningContent)
	assert.Equal(suite.T(), `你好！今天过得怎么样？有什么可以帮你的吗？ 😊`, content)

	reasoningParser.Reset()
	streams = []string{"<th", "i", "nk", ">", "好", "的", "，", "用户", "问", "“你", "好", "吗”", "，", "我需", "要", "以友", "好和亲"}
	reasoningContent, content = "", ""
	for _, text := range streams {
		reasoning, normal := reasoningParser.ExtractReasoningContentStreaming(text)
		reasoningContent += reasoning
		content += normal
	}
	//fmt.Printf("reasoningContent: %s\n, content: %s\n", reasoningContent, content)
	assert.Equal(suite.T(), `好的，用户问“你好吗”，我需要以友好和亲`, reasoningContent)
	assert.Equal(suite.T(), ``, content)

	reasoningParser.Reset()
	streams = []string{"<think>好", "的", "，", "用户", "问", "“你", "</th", "in", "k>aaaa", "\n", "你", "好！", "今天过", "得", "怎么样", "？有", "什", "么可", "以", "帮", "你的", "吗？ 😊"}
	reasoningContent, content = "", ""
	for _, text := range streams {
		reasoning, normal := reasoningParser.ExtractReasoningContentStreaming(text)
		reasoningContent += reasoning
		content += normal
	}
	//fmt.Printf("reasoningContent: %s\n, content: %s\n", reasoningContent, content)

	assert.Equal(suite.T(), "aaaa\n你好！今天过得怎么样？有什么可以帮你的吗？ 😊", content)
	assert.Equal(suite.T(), "好的，用户问“你", reasoningContent)

	reasoningParser.Reset()
	streams = []string{"<think>", "好", "的", "，", "用户", "问", "“你", "好", "吗”", "aa</th", "in", "k>dd", "\n", "你", "好！", "今天过", "得", "怎么样", "？有", "什", "么可", "以", "帮", "你的", "吗？ 😊"}
	reasoningContent, content = "", ""
	for _, text := range streams {
		reasoning, normal := reasoningParser.ExtractReasoningContentStreaming(text)
		reasoningContent += reasoning
		content += normal
	}
	//fmt.Printf("reasoningContent: %s\n, content: %s\n", reasoningContent, content)
	assert.Equal(suite.T(), "dd\n你好！今天过得怎么样？有什么可以帮你的吗？ 😊", content)
	assert.Equal(suite.T(), "好的，用户问“你好吗”aa", reasoningContent)
}

func downloadFile(url, filepath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download file, status code: %d", resp.StatusCode)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

type mockTokenizer struct {
	encodeFunc func(string) ([]uint32, error)
	decodeFunc func([]uint32) string
}

func (m *mockTokenizer) Encode(text string) ([]uint32, error) {
	if m.encodeFunc != nil {
		return m.encodeFunc(text)
	}
	return []uint32{}, nil
}

func (m *mockTokenizer) Decode(ids []uint32) string {
	if m.decodeFunc != nil {
		return m.decodeFunc(ids)
	}
	return ""
}

func (m *mockTokenizer) ConfigPath() string {
	return "/test/tokenizer_config.json"
}

func TestTokenizerSuite(t *testing.T) {
	suite.Run(t, new(TokenizerTestSuite))
}

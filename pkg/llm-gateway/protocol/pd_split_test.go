package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPDSplitDecodeRequestEquivalence(t *testing.T) {
	req := PDSplitCompletionsDecodeRequest{
		PDSplitDecodeBase: PDSplitDecodeBase{
			ID:        "test",
			ChannelID: "ch1",
		},
		Request: CompletionRequest{
			Model:  "gpt-4",
			Prompt: "hello",
		},
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var unmarshaled PDSplitCompletionsDecodeRequest
	err = json.Unmarshal(data, &unmarshaled)
	assert.NoError(t, err)
	assert.Equal(t, req, unmarshaled)
}

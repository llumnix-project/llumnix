package loadbalancer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fastjson"
)

func TestMockRequestFlow(t *testing.T) {
	tests := []struct {
		name    string
		input   []uint32
		wantErr bool
	}{
		{
			name:    "empty array",
			input:   []uint32{},
			wantErr: false,
		},
		{
			name:    "single value",
			input:   []uint32{42},
			wantErr: false,
		},
		{
			name:    "multiple values",
			input:   []uint32{1, 2, 3, 4, 5},
			wantErr: false,
		},
		{
			name:    "large numbers",
			input:   []uint32{0, 65535, 4294967295},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create new request
			req := newMockRequest()

			// Process input data
			mockPreprocess(tt.input, req)

			// Get processed data
			got, err := mockCreateTokenRequest(req)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			// Compare length
			assert.Equal(t, len(tt.input), len(got), "length mismatch")

			// Compare values
			for i := range tt.input {
				assert.Equal(t, int64(tt.input[i]), got[i],
					"value mismatch at index %d: want %d, got %d",
					i, tt.input[i], got[i])
			}
		})
	}
}

func TestMockRequestFlowErrors(t *testing.T) {
	t.Run("missing prompt field", func(t *testing.T) {
		req := newMockRequest()
		_, err := mockCreateTokenRequest(req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "prompt field not found")
	})
}

type MockRequest struct {
	ReqObj *fastjson.Value
}

func newMockRequest() *MockRequest {
	return &MockRequest{
		ReqObj: fastjson.MustParse("{}"),
	}
}

func mockPreprocess(data []uint32, req *MockRequest) {
	arena := fastjson.Arena{}
	arrayValue := arena.NewArray()
	for i, tokenId := range data {
		arrayValue.SetArrayItem(i, arena.NewNumberInt(int(tokenId)))
	}
	req.ReqObj.Set("prompt", arrayValue)
}

func mockCreateTokenRequest(req *MockRequest) ([]int64, error) {
	promptValue := req.ReqObj.Get("prompt")
	if !promptValue.Exists() {
		return nil, fmt.Errorf("prompt field not found")
	}

	arr, err := promptValue.Array()
	if err != nil {
		return nil, fmt.Errorf("prompt is not an array: %v", err)
	}

	data := make([]int64, len(arr))
	for i, v := range arr {
		data[i] = int64(v.GetInt())
	}

	return data, nil
}

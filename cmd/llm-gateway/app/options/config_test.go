package options

import (
	"reflect"
	"testing"
)

func TestSafeSplitArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple key-value pairs",
			input:    "a=1,b=2,c=3",
			expected: []string{"a=1", "b=2", "c=3"},
		},
		{
			name:     "array values with brackets",
			input:    "a=1,b=[1,2,3],c=3",
			expected: []string{"a=1", "b=[1,2,3]", "c=3"},
		},
		{
			name:     "array values with quotes",
			input:    "a=1,b='1,2,3',c=3",
			expected: []string{"a=1", "b=1,2,3", "c=3"},
		},
		{
			name:     "nested arrays",
			input:    "a=1,b=[[1,2],[3,4]],c=3",
			expected: []string{"a=1", "b=[[1,2],[3,4]]", "c=3"},
		},
		{
			name:     "quoted strings",
			input:    "a=1,b='hello,world',c=3",
			expected: []string{"a=1", "b=hello,world", "c=3"},
		},
		{
			name:     "double quoted strings",
			input:    `a=1,b="hello,world",c=3`,
			expected: []string{"a=1", `b=hello,world`, "c=3"},
		},
		{
			name:     "mixed: quotes and brackets",
			input:    "a='x,y',b=[1,2,3],c='hello'",
			expected: []string{"a=x,y", "b=[1,2,3]", "c=hello"},
		},
		{
			name:     "real scenario: mixed types",
			input:    "dispatch-top-k=5,reschedule-policies=[policy1,policy2],timeout='10,20,30'",
			expected: []string{"dispatch-top-k=5", "reschedule-policies=[policy1,policy2]", "timeout=10,20,30"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "single value",
			input:    "a=1",
			expected: []string{"a=1"},
		},
		{
			name:     "trailing comma",
			input:    "a=1,b=2,",
			expected: []string{"a=1", "b=2"},
		},
		{
			name:     "spaces around values",
			input:    "a = 1 , b = 2 , c = 3",
			expected: []string{"a = 1", "b = 2", "c = 3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeSplitArgs(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("safeSplitArgs(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

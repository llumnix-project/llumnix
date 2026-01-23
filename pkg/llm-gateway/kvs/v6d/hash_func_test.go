//go:build cgo && xxhash
// +build cgo,xxhash

package v6d

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashImplementations(t *testing.T) {
	testCases := []struct {
		name            string
		tokens          []int64
		chunkSize       int
		saveUnfullChunk bool
	}{
		{
			name:            "normal case with remainder",
			tokens:          []int64{1, 2, 3, 4, 5},
			chunkSize:       2,
			saveUnfullChunk: true,
		},
		{
			name:            "even blocks",
			tokens:          []int64{1, 2, 3, 4},
			chunkSize:       2,
			saveUnfullChunk: true,
		},
		{
			name:            "empty tokens",
			tokens:          []int64{},
			chunkSize:       2,
			saveUnfullChunk: true,
		},
		{
			name:            "single token",
			tokens:          []int64{1},
			chunkSize:       2,
			saveUnfullChunk: true,
		},
		{
			name:            "chunk size equals tokens length",
			tokens:          []int64{1, 2, 3, 4},
			chunkSize:       4,
			saveUnfullChunk: true,
		},
		{
			name:            "chunk size one",
			tokens:          []int64{1, 2, 3},
			chunkSize:       1,
			saveUnfullChunk: true,
		},
		{
			name:            "large numbers",
			tokens:          []int64{9223372036854775807, -9223372036854775808, 1234567890},
			chunkSize:       2,
			saveUnfullChunk: true,
		},
		{
			name:            "zero values",
			tokens:          []int64{0, 0, 0, 0, 0},
			chunkSize:       2,
			saveUnfullChunk: true,
		},
		{
			name:            "mixed positive negative zero",
			tokens:          []int64{-1, 0, 1, -2, 0, 2},
			chunkSize:       2,
			saveUnfullChunk: true,
		},
		{
			name:            "large chunk size",
			tokens:          []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			chunkSize:       7,
			saveUnfullChunk: true,
		},
		{
			name:            "large input small chunks",
			tokens:          generateLargeInput(100),
			chunkSize:       3,
			saveUnfullChunk: true,
		},
		{
			name:            "repeating pattern",
			tokens:          []int64{1, 2, 3, 1, 2, 3, 1, 2, 3},
			chunkSize:       3,
			saveUnfullChunk: true,
		},
		{
			name:            "sequential numbers with small chunk",
			tokens:          generateSequentialNumbers(20),
			chunkSize:       4,
			saveUnfullChunk: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Go implementation
			goHashes, err := GoHashV6d(tc.tokens, tc.chunkSize, tc.saveUnfullChunk)
			assert.NoError(t, err)

			// C implementation
			cHashes, err := CHashV6d(tc.tokens, tc.chunkSize)
			assert.NoError(t, err)

			// Compare results
			assert.Equal(t, len(goHashes), len(cHashes), "Hash count should match for case: %s", tc.name)
			for i := range goHashes {
				assert.Equal(t, goHashes[i], cHashes[i],
					"Hash at index %d should match for case: %s\nTokens: %v\nChunk size: %d",
					i, tc.name, tc.tokens, tc.chunkSize)
			}

			t.Logf("Case: %s", tc.name)
			t.Logf("Tokens: %v", tc.tokens)
			t.Logf("Chunk size: %d", tc.chunkSize)
			t.Logf("Go hashes: %v", goHashes)
			t.Logf("C  hashes: %v", cHashes)
		})
	}
}

func generateLargeInput(size int) []int64 {
	result := make([]int64, size)
	for i := 0; i < size; i++ {
		result[i] = int64((i*17 + 11) % 100)
	}
	return result
}

func generateSequentialNumbers(size int) []int64 {
	result := make([]int64, size)
	for i := 0; i < size; i++ {
		result[i] = int64(i)
	}
	return result
}

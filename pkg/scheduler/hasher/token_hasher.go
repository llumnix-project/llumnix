package hasher

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"

	"llumnix/pkg/consts"
)

// TokenHasher dispatches token hashing to the appropriate algorithm based on hashAlgo.
type TokenHasher struct {
	hashAlgo string
}

// NewTokenHasher creates a TokenHasher for the given algorithm.
// Supported values: "sha256_hex", "sha256_cbor", "xxhash".
func NewTokenHasher(hashAlgo string) (*TokenHasher, error) {
	switch hashAlgo {
	case consts.KvsHashAlgoSha256Hex,
		consts.KvsHashAlgoSha256CBOR,
		consts.KvsHashAlgoXxhash:
	default:
		return nil, fmt.Errorf("unsupported hash algorithm: %s", hashAlgo)
	}
	return &TokenHasher{hashAlgo: hashAlgo}, nil
}

// HashTokens computes block hash keys for the given tokens.
// The output format depends on the configured algorithm:
//   - sha256_hex:  hex(sha256(tokens_per_block)), last unfull chunk appended with "_0"
//   - sha256_cbor: hex(sha256(cbor([prefixHash, chunk, nil]))) + "_0"
//   - xxhash:      irisMetaPrefix + vLLMBlockPrefix + decimal(xxhash64)
func (h *TokenHasher) HashTokens(
	tokens []int64, chunkSize int, saveUnfullChunk bool,
	irisMetaPrefix string, vLLMBlockPrefix string,
) ([]string, error) {
	if len(tokens) == 0 || chunkSize <= 0 {
		return nil, fmt.Errorf("invalid hash input")
	}

	switch h.hashAlgo {
	case consts.KvsHashAlgoSha256Hex:
		return h.hashTokensSha256Hex(tokens, chunkSize, saveUnfullChunk)
	case consts.KvsHashAlgoSha256CBOR:
		return h.hashTokensSha256CBOR(tokens, chunkSize, saveUnfullChunk)
	case consts.KvsHashAlgoXxhash:
		return h.hashTokensXxhash(tokens, chunkSize, saveUnfullChunk, irisMetaPrefix, vLLMBlockPrefix)
	default:
		return nil, fmt.Errorf("unsupported hash algorithm: %s", h.hashAlgo)
	}
}

func (h *TokenHasher) hashTokensSha256Hex(
	tokens []int64, chunkSize int, saveUnfullChunk bool,
) ([]string, error) {
	numCompleteBlocks := len(tokens) / chunkSize
	remainder := len(tokens) % chunkSize
	totalBlocks := numCompleteBlocks
	if saveUnfullChunk && remainder > 0 {
		totalBlocks++
	}
	if totalBlocks == 0 {
		return []string{}, nil
	}

	blockHashes := make([]string, 0, totalBlocks)

	prefixHash := ""
	for i := 0; i < numCompleteBlocks; i++ {
		chunk := tokens[i*chunkSize : (i+1)*chunkSize]
		h, err := HashBlockSha256Hex(chunk, prefixHash)
		if err != nil {
			return nil, err
		}
		blockHashes = append(blockHashes, h)
		prefixHash = h
	}

	if saveUnfullChunk && remainder > 0 {
		chunk := tokens[numCompleteBlocks*chunkSize:]
		h, err := HashBlockSha256Hex(chunk, prefixHash)
		if err != nil {
			return nil, err
		}
		// hard code, keys saved by hybrid connector have tp_rank suffix, we use keys of tp rank 0 worker to query
		blockHashes = append(blockHashes, h+"_0")
	}

	return blockHashes, nil
}

func (h *TokenHasher) hashTokensSha256CBOR(
	tokens []int64, chunkSize int, saveUnfullChunk bool,
) ([]string, error) {
	numCompleteBlocks := len(tokens) / chunkSize
	remainder := len(tokens) % chunkSize
	totalBlocks := numCompleteBlocks
	if remainder > 0 && saveUnfullChunk {
		totalBlocks++
	}
	if totalBlocks == 0 {
		return []string{}, nil
	}

	seed, ok := os.LookupEnv("GO_HASH_SEED")
	if !ok || seed == "" {
		seed = "0"
	}
	prefixHash, err := Sha256CBOR(seed)
	if err != nil {
		return nil, err
	}

	blockHashes := make([]string, 0, totalBlocks)

	for i := 0; i < totalBlocks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(tokens) {
			end = len(tokens)
		}
		chunk := tokens[start:end]
		digest, err := Sha256CBOR([]any{prefixHash, chunk, nil})
		if err != nil {
			return nil, err
		}
		prefixHash = digest
		// hard code, keys saved by hybrid connector have tp_rank suffix, we use keys of tp rank 0 worker to query
		blockHashes = append(blockHashes, hex.EncodeToString(prefixHash)+"_0")
	}

	return blockHashes, nil
}

func buildBlockName(irisMetaPrefix, vLLMBlockPrefix string, hash uint64) string {
	return irisMetaPrefix + vLLMBlockPrefix + strconv.FormatUint(hash, 10)
}

func (h *TokenHasher) hashTokensXxhash(
	tokens []int64, chunkSize int, saveUnfullChunk bool,
	irisMetaPrefix string, vLLMBlockPrefix string,
) ([]string, error) {
	rawHashes, err := HashTokensXxhash(tokens, chunkSize, saveUnfullChunk)
	if err != nil {
		return nil, err
	}

	blockHashes := make([]string, len(rawHashes))
	for i, hash := range rawHashes {
		blockHashes[i] = buildBlockName(irisMetaPrefix, vLLMBlockPrefix, hash)
	}
	return blockHashes, nil
}

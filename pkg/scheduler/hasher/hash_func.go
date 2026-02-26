package hasher

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/cespare/xxhash/v2"
	"github.com/fxamacker/cbor/v2"
)

// sha256CBOR serializes v with canonical CBOR (RFC 8949 canonical form)
// and returns SHA-256 digest bytes (32 bytes), matching:
//
//	cbor2.dumps(input, canonical=True)
//	hashlib.sha256(input_bytes).digest()
func Sha256CBOR(v any) ([]byte, error) {
	// Canonical CBOR encoding (deterministic, sorted map keys, shortest forms, etc.)
	encMode, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		return nil, err
	}
	b, err := encMode.Marshal(v)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}

// hashBlockSha256Hex replicates Python:
//
// def _get_hash_str(token_ids, prior_hash=None):
//
//	hasher = hashlib.sha256()
//	if prior_hash: hasher.update(bytes.fromhex(prior_hash))
//	for t in token_ids:
//	  if isinstance(t, tuple):
//	    for elem in t: hasher.update(elem.to_bytes(4,"little",signed=False))
//	  else:
//	    hasher.update(t.to_bytes(4,"little",signed=False))
//	return hasher.hexdigest()
func HashBlockSha256Hex(tokens []int64, priorHex string) (string, error) {
	h := sha256.New()

	if priorHex != "" {
		priorBytes, err := hex.DecodeString(priorHex)
		if err != nil {
			return "", fmt.Errorf("invalid prior_hash hex: %w", err)
		}
		if len(priorBytes) != sha256.Size {
			return "", fmt.Errorf("prior_hash must decode to %d bytes, got %d", sha256.Size, len(priorBytes))
		}
		_, _ = h.Write(priorBytes)
	}

	var buf [4]byte
	for _, t := range tokens {
		// to_bytes(4, signed=False) requires 0 <= t <= 2^32-1
		if t < 0 || t > int64(^uint32(0)) {
			return "", fmt.Errorf("token out of uint32 range: %d", t)
		}
		binary.LittleEndian.PutUint32(buf[:], uint32(t))
		_, _ = h.Write(buf[:])
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashTokensXxhash is the Go implementation without buildBlockName
func HashTokensXxhash(tokens []int64, chunkSize int, saveUnfullChunk bool) ([]uint64, error) {
	if len(tokens) == 0 || chunkSize <= 0 {
		return []uint64{}, nil
	}

	numCompleteBlocks := len(tokens) / chunkSize
	totalBlocks := numCompleteBlocks
	remainder := len(tokens) % chunkSize
	if remainder > 0 {
		totalBlocks++
	}

	blockHashes := make([]uint64, 0, totalBlocks)
	hasher := xxhash.NewWithSeed(0)
	defer hasher.Reset()

	// Process complete blocks
	for i := 0; i < numCompleteBlocks; i++ {
		if err := binary.Write(hasher, binary.LittleEndian, tokens[i*chunkSize:(i+1)*chunkSize]); err != nil {
			return nil, err
		}
		blockHashes = append(blockHashes, hasher.Sum64())
	}

	// Process last incomplete block if it exists
	if saveUnfullChunk && remainder > 0 {
		if err := binary.Write(hasher, binary.LittleEndian, tokens[numCompleteBlocks*chunkSize:]); err != nil {
			return nil, err
		}

		padding := make([]int64, chunkSize-remainder)
		if err := binary.Write(hasher, binary.LittleEndian, padding); err != nil {
			return nil, err
		}

		blockHashes = append(blockHashes, hasher.Sum64())
	}

	return blockHashes, nil
}

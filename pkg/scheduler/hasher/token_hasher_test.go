package hasher

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/consts"
)

// ---- NewTokenHasher ----

func TestNewTokenHasher_Supported(t *testing.T) {
	for _, algo := range []string{
		consts.KvsHashAlgoSha256Hex,
		consts.KvsHashAlgoSha256CBOR,
		consts.KvsHashAlgoXxhash,
	} {
		th, err := NewTokenHasher(algo)
		if err != nil {
			t.Fatalf("NewTokenHasher(%s) err=%v", algo, err)
		}
		if th == nil {
			t.Fatalf("NewTokenHasher(%s) returned nil", algo)
		}
	}
}

func TestNewTokenHasher_Unsupported(t *testing.T) {
	_, err := NewTokenHasher("md5")
	if err == nil {
		t.Fatalf("expected error for unsupported algo")
	}
}

// ---- sha256_cbor hash tests (migrated from mooncake/meta_service_client_test.go) ----

func TestTokenHasher_Sha256CBOR_InvalidInput(t *testing.T) {
	th, _ := NewTokenHasher(consts.KvsHashAlgoSha256CBOR)
	_, err := th.HashTokens(nil, 4, true, "iris_", "vllm_")
	if err == nil {
		t.Fatalf("expected error for empty tokens")
	}
	_, err = th.HashTokens([]int64{1, 2, 3}, 0, true, "iris_", "vllm_")
	if err == nil {
		t.Fatalf("expected error for chunkSize<=0")
	}
}

func TestTokenHasher_Sha256CBOR_Chunking_SaveUnfullChunk(t *testing.T) {
	t.Setenv("GO_HASH_SEED", "seed-1")
	th, _ := NewTokenHasher(consts.KvsHashAlgoSha256CBOR)
	tokens := []int64{1, 2, 3, 4, 5} // chunkSize=4 => 1 full + remainder
	hs1, err := th.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	if len(hs1) != 2 {
		t.Fatalf("expected 2 hashes, got %d: %v", len(hs1), hs1)
	}
	hs2, err := th.HashTokens(tokens, 4, false, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	if len(hs2) != 1 {
		t.Fatalf("expected 1 hash, got %d: %v", len(hs2), hs2)
	}
}

func TestTokenHasher_Sha256CBOR_DeterministicWithSameSeed(t *testing.T) {
	t.Setenv("GO_HASH_SEED", "seed-xyz")
	th, _ := NewTokenHasher(consts.KvsHashAlgoSha256CBOR)
	tokens := []int64{10, 11, 12, 13}
	h1, err := th.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	h2, err := th.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	if len(h1) != len(h2) || h1[0] != h2[0] {
		t.Fatalf("expected deterministic hashes, got h1=%v h2=%v", h1, h2)
	}
}

func TestTokenHasher_Sha256CBOR_DifferentSeedDifferentResult(t *testing.T) {
	th, _ := NewTokenHasher(consts.KvsHashAlgoSha256CBOR)
	tokens := []int64{10, 11, 12, 13}
	os.Setenv("GO_HASH_SEED", "seed-a")
	h1, err := th.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	os.Setenv("GO_HASH_SEED", "seed-b")
	h2, err := th.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	if h1[0] == h2[0] {
		t.Fatalf("expected different hashes for different seeds, h1=%v h2=%v", h1, h2)
	}
}

// ---- xxhash hash tests (migrated from v6d/metadata_service_client_test.go) ----

func TestTokenHasher_Xxhash(t *testing.T) {
	th, _ := NewTokenHasher(consts.KvsHashAlgoXxhash)

	t.Run("invalid input", func(t *testing.T) {
		_, err := th.HashTokens(nil, 4, true, "iris_", "vllm_")
		assert.Error(t, err)

		_, err = th.HashTokens([]int64{1, 2, 3}, 0, true, "iris_", "vllm_")
		assert.Error(t, err)
	})

	t.Run("chunking and saveUnfullChunk", func(t *testing.T) {
		tokens := []int64{1, 2, 3, 4, 5} // chunkSize=4 => 1 full + 1 remainder

		hs1, err := th.HashTokens(tokens, 4, true, "iris_", "vllm_")
		assert.NoError(t, err)
		assert.Len(t, hs1, 2)

		hs2, err := th.HashTokens(tokens, 4, false, "iris_", "vllm_")
		assert.NoError(t, err)
		assert.Len(t, hs2, 1)

		// same input should be deterministic
		hs1Again, err := th.HashTokens(tokens, 4, true, "iris_", "vllm_")
		assert.NoError(t, err)
		assert.Equal(t, hs1, hs1Again)
	})

	t.Run("prefix formatting", func(t *testing.T) {
		hs, err := th.HashTokens([]int64{1, 2, 3, 4}, 4, true, "iris_meta_", "vllm_block_")
		assert.NoError(t, err)
		assert.Len(t, hs, 1)
		assert.Contains(t, hs[0], "iris_meta_"+"vllm_block_")
	})
}

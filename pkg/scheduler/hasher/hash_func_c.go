//go:build cgo && xxhash
// +build cgo,xxhash

package hasher

/*
#cgo CFLAGS: -I/opt/homebrew/include
#cgo LDFLAGS: -L/opt/homebrew/lib -lxxhash
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "xxhash.h"

uint64_t* GetBlockHashesFromTokens_PrefixHash(int block_size, int64_t* tokens, size_t tokens_size, size_t* out_size) {
    if (tokens_size == 0 || block_size <= 0) {
        *out_size = 0;
        return NULL;
    }

    size_t num_complete_blocks = tokens_size / block_size;
    size_t remainder = tokens_size % block_size;
    size_t total_blocks = num_complete_blocks + (remainder > 0 ? 1 : 0);

    uint64_t* block_hashes = (uint64_t*)malloc(total_blocks * sizeof(uint64_t));
    *out_size = total_blocks;

    XXH64_state_t* hash_state = XXH64_createState();
    XXH64_reset(hash_state, 0);

    // Process complete blocks
    for (size_t i = 0; i < num_complete_blocks; ++i) {
        XXH64_update(hash_state,
                     (const char*)&tokens[i * block_size],
                     block_size * sizeof(int64_t));
        block_hashes[i] = XXH64_digest(hash_state);
    }

    // Process last incomplete block if it exists
    if (remainder > 0) {
        // Add remaining tokens
        XXH64_update(hash_state,
                     (const char*)&tokens[num_complete_blocks * block_size],
                     remainder * sizeof(int64_t));

        // Pad with zeros to match original behavior
        int64_t pack = 0;
        size_t num_packed = block_size - remainder;
        for (size_t i = 0; i < num_packed; ++i) {
            XXH64_update(hash_state, (const char*)&pack, sizeof(int64_t));
        }
        block_hashes[num_complete_blocks] = XXH64_digest(hash_state);
    }

    XXH64_freeState(hash_state);
    return block_hashes;
}
*/
import "C"
import "unsafe"

// CHashV6d wraps the C implementation for cross-language verification.
func CHashV6d(tokens []int64, chunkSize int) ([]uint64, error) {
	if len(tokens) == 0 || chunkSize <= 0 {
		return []uint64{}, nil
	}

	var outSize C.size_t
	cTokens := (*C.int64_t)(unsafe.Pointer(&tokens[0]))
	cHashes := C.GetBlockHashesFromTokens_PrefixHash(C.int(chunkSize), cTokens, C.size_t(len(tokens)), &outSize)
	defer C.free(unsafe.Pointer(cHashes))

	cHashesSlice := make([]uint64, outSize)
	cArray := (*[1 << 30]C.uint64_t)(unsafe.Pointer(cHashes))[:outSize:outSize]
	for i := range cArray {
		cHashesSlice[i] = uint64(cArray[i])
	}

	return cHashesSlice, nil
}

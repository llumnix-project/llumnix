package kvs

type KVSMetadataServiceClientInterface interface {
	// HashTokens computes the hash values for the given tokens by dividing them into blocks of size chunkSize.
	// Each block is hashed using XXHash64, and the resulting hash is converted to a string.
	// If the tokens slice is empty or chunkSize is non-positive, an empty slice is returned.
	//
	// Parameters:
	//   - tokens: A slice of int64 representing the input tokens to be hashed.
	//
	// Returns:
	//   - []string: A slice of strings where each string represents the hash of a block of tokens.
	//   - error: An error if there was a problem writing to the hasher, otherwise nil.
	//
	// NOTE(sunbiao.sun): v6d lack of e2e hash function, this function is deducted manually based on
	// GetBlockHashesFromTokens_PrefixHash function in MetadataService, so the correctness should be tested in e2e test.
	HashTokens(tokens []int64, chunkSize int, saveUnfullChunk bool, irisMetaPrefix string, vLLMBlockPrefix string) ([]string, error)

	// BatchQueryPrefixHashHitKVSInstances retrieves the list of kvs instances (identified by string) associated with multiple hash keys from the kvs metadata service.
	// Parameters:
	//   - hashKeys: A slice of strings representing the hash keys to query.
	//
	// Returns:
	//   - map[string][]string: A map where the key is the hash key and the value is a slice of strings representing the kvs instances associated with that hash key.
	//   - error: An error if the query fails, otherwise nil.
	BatchQueryPrefixHashHitKVSInstances(hashKeys []string) (map[string][]string, error)
}

package kvs

type KVSMetadataServiceClientInterface interface {
	// BatchQueryPrefixHashHitKVSInstances retrieves the list of kvs instances (identified by string) associated with multiple hash keys from the kvs metadata service.
	// Parameters:
	//   - hashKeys: A slice of strings representing the hash keys to query.
	//
	// Returns:
	//   - map[string][]string: A map where the key is the hash key and the value is a slice of strings representing the kvs instances associated with that hash key.
	//   - error: An error if the query fails, otherwise nil.
	BatchQueryPrefixHashHitKVSInstances(hashKeys []string) (map[string][]string, error)
}

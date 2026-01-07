package gron

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// UngronHelper transforms kv-string (such as 'abool = true') list to gron content like testdata/a.gron
func UngronHelper(kvs []string) string {
	gronList := []string{"json = {};"}
	keyRecord := map[string]bool{}
	var prefixKey string
	for _, config := range kvs {
		kv := strings.Split(config, " = ")
		if len(kv) < 2 {
			continue
		}
		gronList = append(gronList, fmt.Sprintf("json.%s;", config))

		keys := strings.Split(kv[0], ".")
		last := keys[len(keys)-1]
		for id, key := range keys[:len(keys)-1] {
			if strings.Contains(key, "[") && strings.Contains(key, "]") {
				isListType := isInvalidSliceIndex(key)
				ks := strings.Split(key, "[")
				if len(ks) < 2 {
					// skip this invalid key
					break
				}
				prefixKey = strings.Join(keys[:id], ".")
				if len(prefixKey) == 0 {
					prefixKey = ks[0]
				} else {
					prefixKey = strings.Join([]string{prefixKey, ks[0]}, ".")
				}
				if !keyRecord[prefixKey] {
					if isListType {
						gronList = append(gronList, fmt.Sprintf("json.%s = [];", prefixKey))
					} else {
						gronList = append(gronList, fmt.Sprintf("json.%s = {};", prefixKey))
					}
					keyRecord[prefixKey] = true
				}

				listKey := strings.Join(keys[:id+1], ".")
				if !keyRecord[listKey] {
					// last item is list val
					if strings.Contains(last, "[") && strings.Contains(last, "]") {
						gronList = append(gronList, fmt.Sprintf("json.%s = [];", listKey))
					} else {
						gronList = append(gronList, fmt.Sprintf("json.%s = {};", listKey))
					}
					keyRecord[listKey] = true
				}

			} else {
				prefixKey = strings.Join(keys[:id+1], ".")
				if !keyRecord[prefixKey] && !strings.Contains(prefixKey, "[\"") {
					gronList = append(gronList, fmt.Sprintf("json.%s = {};", prefixKey))
					keyRecord[prefixKey] = true
				}
			}
		}
		if strings.Contains(last, "[") && strings.Contains(last, "]") {
			isSliceType := isInvalidSliceIndex(last)
			ks := strings.Split(last, "[")
			if len(ks) < 2 {
				// skip this invalid key
				break
			}
			prefixKey = strings.Join(append(keys[:len(keys)-1], ks[0]), ".")
			if len(keys) == 1 {
				prefixKey = ks[0]
			}
			if !keyRecord[prefixKey] {
				if isSliceType {
					gronList = append(gronList, fmt.Sprintf("json.%s = [];", prefixKey))
				} else {
					gronList = append(gronList, fmt.Sprintf("json.%s = {};", prefixKey))
				}
				keyRecord[prefixKey] = true
			}
		}
	}
	sort.Strings(gronList)
	return strings.Join(gronList, "\n")
}

func isInvalidSliceIndex(key string) bool {
	stIndex := strings.Index(key, "[")
	edIndex := strings.LastIndex(key, "]")
	if stIndex+1 >= len(key) {
		return false
	}
	idxVal := key[stIndex+1 : edIndex]

	if _, checkIndexErr := strconv.Atoi(idxVal); checkIndexErr != nil {
		return false
	}
	return true
}

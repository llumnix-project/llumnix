package maps

import "dario.cat/mergo"

// ReadOnlyMap defines a read-only map interface that only supports Get and Iterate operations.
type ReadOnlyMap[K comparable, V any] interface {
	ValueOf(key K) V
	// Get returns the value associated with the given key and a boolean indicating if the key exists.
	Get(key K) (V, bool)
	// Iterate traverses all key-value pairs in the map, calling the provided function for each pair.
	// If the function returns false, the iteration stops.
	Iterate(func(K, V) bool)
	// Map returns a copy of the underlying map.
	Map() map[K]V
}

// readOnlyMap is an implementation of the ReadOnlyMap interface.
type readOnlyMap[K comparable, V any] struct {
	data       map[K]V
	defaultVal V
	hasDefault bool
}

// NewReadOnlyMap creates a new ReadOnlyMap instance.
// It makes a copy of the input map to ensure thread-safety and prevent external modifications.
func NewReadOnlyMap[K comparable, V any](data map[K]V) ReadOnlyMap[K, V] {
	// Create a copy of the map to ensure that modifications to the original map
	// after construction don't affect the ReadOnlyMap
	copiedData := make(map[K]V, len(data))
	for k, v := range data {
		copiedData[k] = v
	}

	return &readOnlyMap[K, V]{
		data: copiedData,
	}
}

func NewReadOnlyMapWithDefault[K comparable, V any](data map[K]V, defaultVal V) ReadOnlyMap[K, V] {
	// Create a copy of the map to ensure that modifications to the original map
	// after construction don't affect the ReadOnlyMap
	copiedData := make(map[K]V, len(data))
	for k, v := range data {
		copiedData[k] = v
	}

	return &readOnlyMap[K, V]{
		data:       copiedData,
		defaultVal: defaultVal,
		hasDefault: true,
	}
}

// ValueOf returns the value associated with the given key.
func (m *readOnlyMap[K, V]) ValueOf(key K) V {
	v, ok := m.data[key]
	if ok {
		return v
	}
	if m.hasDefault {
		return m.defaultVal
	}
	return v
}

// Get returns the value associated with the given key and a boolean indicating if the key exists.
func (m *readOnlyMap[K, V]) Get(key K) (V, bool) {
	value, exists := m.data[key]
	return value, exists
}

// Iterate traverses all key-value pairs in the map, calling the provided function for each pair.
// If the function returns false, the iteration stops.
func (m *readOnlyMap[K, V]) Iterate(f func(K, V) bool) {
	for k, v := range m.data {
		if !f(k, v) {
			break
		}
	}
}

// Map returns a copy of the underlying map.
func (m *readOnlyMap[K, V]) Map() map[K]V {
	copiedData := make(map[K]V, len(m.data))
	for k, v := range m.data {
		copiedData[k] = v
	}
	return copiedData
}

func Ensure[M ~map[K]V, K comparable, V any](m M) M {
	if m != nil {
		return m
	} else {
		return make(M)
	}
}

func EnsurePtr[M ~map[K]V, K comparable, V any](m *M) M {
	if *m == nil {
		*m = make(M)
	}
	return *m
}

func Keys[M ~map[K]V, K comparable, V any](m M) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func Values[M ~map[K]V, K comparable, V any](m M) []V {
	values := make([]V, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}

func First[M ~map[K]V, K comparable, V any](key K, mm ...M) (V, bool) {
	for _, m := range mm {
		if v, ok := m[key]; ok {
			return v, true
		}
	}
	return *new(V), false
}

func Append[M ~map[K]V, K comparable, V ~[]E, E any](m M, key K, value E) M {
	if m == nil {
		m = make(M)
	}
	if v, ok := m[key]; ok {
		m[key] = append(v, value)
	} else {
		m[key] = []E{value}
	}
	return m
}

func Call[M ~map[K]V, K comparable, V any](m M, key K, f func(V) V) (V, bool) {
	var ret V
	var exist bool
	if v, ok := m[key]; ok {
		exist = true
		ret = f(v)
		m[key] = ret
	}
	return ret, exist
}

func Set[M ~map[K]V, K comparable, V any](m M, key K, value V) M {
	if m == nil {
		m = make(M)
	}
	m[key] = value
	return m
}

func Merge[M ~map[K]V, K comparable, V any](dst M, src M) M {
	if dst == nil {
		return src
	}

	if src == nil {
		return dst
	}

	mergo.Merge(&dst, src)
	return dst
}

package maps

import (
	"dario.cat/mergo"
	"testing"
)

func TestEnsure(t *testing.T) {
	var m map[string]int
	if m != nil {
		t.Fatal("map should be nil")
	}
	m = Ensure(m)
	if m == nil {
		t.Fatal("map should not be nil")
	}
}

func TestEnsurePtr(t *testing.T) {
	var m map[string]int
	if m != nil {
		t.Fatal("map should be nil")
	}
	c := EnsurePtr(&m)
	if c == nil {
		t.Fatal("map should not be nil")
	}
	if m == nil {
		t.Fatal("map should not be nil")
	}
}

func TestMergoMerge(t *testing.T) {
	src := map[string]int{"a": 1, "b": 2}
	dst := map[string]int{"b": 3, "c": 4}

	err := mergo.Merge(&dst, &src)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(src)
	t.Log(dst)

	if dst["a"] != 1 {
		t.Fatal("dst['a'] should be 1")
	}
	if dst["b"] != 3 {
		t.Fatal("dst['b'] should be 3")
	}
	if dst["c"] != 4 {
		t.Fatal("dst['c'] should be 4")
	}
}

func TestMergoMergeWithOverride(t *testing.T) {
	src := map[string]int{"a": 1, "b": 2}
	dst := map[string]int{"b": 3, "c": 4}

	err := mergo.Merge(&dst, &src, mergo.WithOverride)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(src)
	t.Log(dst)

	if dst["a"] != 1 {
		t.Fatal("dst['a'] should be 1")
	}
	if dst["b"] != 2 {
		t.Fatal("dst['b'] should be 2")
	}
	if dst["c"] != 4 {
		t.Fatal("dst['c'] should be 4")
	}
}

func TestMergoMerge2(t *testing.T) {
	var src map[string]int
	dst := map[string]int{"b": 3, "c": 4}

	err := mergo.Merge(&dst, src)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(dst)
}

func TestMergoMerge3(t *testing.T) {
	type S struct {
		m map[string]int
	}

	src := map[string]int{"a": 1, "b": 2}
	s := S{}
	if s.m == nil {
		t.Log("s.m is nil")
	}
	EnsurePtr(&s.m)
	if s.m != nil {
		t.Log("s.m is not nil")
	}

	err := mergo.Merge(&s.m, &src)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(s.m)
}

func TestMerge(t *testing.T) {
	src := map[string]int{"a": 1, "b": 2, "c": 3, "d": 4}
	dst := map[string]int{"b": 3, "c": 4}
	dst = Merge(dst, src)
	t.Log(dst)

	if dst["a"] != 1 {
		t.Fatal("dst['a'] should be 1")
	}
	if dst["b"] != 3 {
		t.Fatal("dst['b'] should be 3")
	}
	if dst["c"] != 4 {
		t.Fatal("dst['c'] should be 4")
	}
	if dst["d"] != 4 {
		t.Fatal("dst['d'] should be 4")
	}
}

// TestReadOnlyMap_Get tests the Get method of ReadOnlyMap.
// Test case 1: Get value with existing key
// Test case 2: Get value with non-existing key
func TestReadOnlyMap_Get(t *testing.T) {
	data := map[string]int{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	rom := NewReadOnlyMap(data)

	// Test case 1: Get value with existing key
	if v, ok := rom.Get("a"); !ok || v != 1 {
		t.Errorf("Expected (1, true), got (%v, %v)", v, ok)
	}

	// Test case 2: Get value with non-existing key
	if v, ok := rom.Get("d"); ok || v != 0 {
		t.Errorf("Expected (0, false), got (%v, %v)", v, ok)
	}
}

// TestReadOnlyMap_Iterate tests the Iterate method of ReadOnlyMap.
// It verifies that all key-value pairs are correctly iterated.
func TestReadOnlyMap_Iterate(t *testing.T) {
	data := map[string]int{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	rom := NewReadOnlyMap(data)

	count := 0
	rom.Iterate(func(k string, v int) bool {
		count++
		switch k {
		case "a":
			if v != 1 {
				t.Errorf("Expected 1 for key 'a', got %v", v)
			}
		case "b":
			if v != 2 {
				t.Errorf("Expected 2 for key 'b', got %v", v)
			}
		case "c":
			if v != 3 {
				t.Errorf("Expected 3 for key 'c', got %v", v)
			}
		default:
			t.Errorf("Unexpected key: %v", k)
		}
		return true
	})

	if count != 3 {
		t.Errorf("Expected to iterate 3 times, got %v", count)
	}
}

// TestReadOnlyMap_IterateStop tests that the Iterate method can be stopped by returning false.
func TestReadOnlyMap_IterateStop(t *testing.T) {
	data := map[string]int{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	rom := NewReadOnlyMap(data)

	count := 0
	rom.Iterate(func(k string, v int) bool {
		count++
		return false // Stop iteration
	})

	if count != 1 {
		t.Errorf("Expected to iterate 1 time, got %v", count)
	}
}

// TestReadOnlyMap_Map tests the Map method of ReadOnlyMap.
// It verifies that the returned map is a copy and modifications to it do not affect the original.
func TestReadOnlyMap_Map(t *testing.T) {
	data := map[string]int{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	rom := NewReadOnlyMap(data)
	copiedMap := rom.Map()

	// Verify that the copied map has the same content
	if len(copiedMap) != len(data) {
		t.Errorf("Expected copied map length %d, got %d", len(data), len(copiedMap))
	}

	for k, v := range data {
		if cv, ok := copiedMap[k]; !ok || cv != v {
			t.Errorf("Expected key %s to have value %d, got %d", k, v, cv)
		}
	}

	// Modify the copied map
	copiedMap["d"] = 4
	copiedMap["a"] = 10

	// Verify that the original data was not affected
	if len(data) != 3 {
		t.Errorf("Expected original map length 3, got %d", len(data))
	}

	if data["a"] != 1 {
		t.Errorf("Expected original map value for 'a' to be 1, got %d", data["a"])
	}

	if _, exists := data["d"]; exists {
		t.Error("Expected key 'd' not to exist in original map")
	}

	// Verify that the ReadOnlyMap was not affected
	if v, ok := rom.Get("a"); !ok || v != 1 {
		t.Errorf("Expected ReadOnlyMap value for 'a' to be 1, got %d", v)
	}

	if _, ok := rom.Get("d"); ok {
		t.Error("Expected key 'd' not to exist in ReadOnlyMap")
	}
}

// TestReadOnlyMap_NewReadOnlyMapCopy tests that NewReadOnlyMap creates a copy of the input map.
// It verifies that modifications to the original map do not affect the ReadOnlyMap.
func TestReadOnlyMap_NewReadOnlyMapCopy(t *testing.T) {
	data := map[string]int{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	rom := NewReadOnlyMap(data)

	// Modify the original map
	data["d"] = 4
	data["a"] = 10
	delete(data, "b")

	// Verify that the ReadOnlyMap was not affected by modifications to the original map
	if v, ok := rom.Get("a"); !ok || v != 1 {
		t.Errorf("Expected ReadOnlyMap value for 'a' to be 1, got %d", v)
	}

	if _, ok := rom.Get("d"); ok {
		t.Error("Expected key 'd' not to exist in ReadOnlyMap")
	}

	if v, ok := rom.Get("b"); !ok || v != 2 {
		t.Errorf("Expected ReadOnlyMap value for 'b' to be 2, got %d", v)
	}

	if v, ok := rom.Get("c"); !ok || v != 3 {
		t.Errorf("Expected ReadOnlyMap value for 'c' to be 3, got %d", v)
	}
}

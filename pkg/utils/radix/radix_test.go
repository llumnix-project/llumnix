package radix

import (
	crand "crypto/rand"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func TestRadix(t *testing.T) {
	var min, max string
	inp := make(map[string]interface{})
	for i := 0; i < 1000; i++ {
		gen := generateUUID()
		inp[gen] = i
		if gen < min || i == 0 {
			min = gen
		}
		if gen > max || i == 0 {
			max = gen
		}
	}

	r := NewFromMap(inp)
	if r.Len() != len(inp) {
		t.Fatalf("bad length: %v %v", r.Len(), len(inp))
	}

	r.Walk(func(k string, v interface{}) bool {
		println(k)
		return false
	})

	for k, v := range inp {
		out, ok := r.Get(k)
		if !ok {
			t.Fatalf("missing key: %v", k)
		}
		if out != v {
			t.Fatalf("value mis-match: %v %v", out, v)
		}
	}

	// Check min and max
	outMin, _, _ := r.Minimum()
	if outMin != min {
		t.Fatalf("bad minimum: %v %v", outMin, min)
	}
	outMax, _, _ := r.Maximum()
	if outMax != max {
		t.Fatalf("bad maximum: %v %v", outMax, max)
	}

	for k, v := range inp {
		out, ok := r.Delete(k)
		if !ok {
			t.Fatalf("missing key: %v", k)
		}
		if out != v {
			t.Fatalf("value mis-match: %v %v", out, v)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("bad length: %v", r.Len())
	}
}

func TestRoot(t *testing.T) {
	r := New()
	_, ok := r.Delete("")
	if ok {
		t.Fatalf("bad")
	}
	_, ok = r.Insert("", true)
	if ok {
		t.Fatalf("bad")
	}
	val, ok := r.Get("")
	if !ok || val != true {
		t.Fatalf("bad: %v", val)
	}
	val, ok = r.Delete("")
	if !ok || val != true {
		t.Fatalf("bad: %v", val)
	}
}

func TestDelete(t *testing.T) {

	r := New()

	s := []string{"", "A", "AB"}

	for _, ss := range s {
		r.Insert(ss, true)
	}

	for _, ss := range s {
		_, ok := r.Delete(ss)
		if !ok {
			t.Fatalf("bad %q", ss)
		}
	}
}

func TestDeletePrefix(t *testing.T) {
	type exp struct {
		inp        []string
		prefix     string
		out        []string
		numDeleted int
	}

	cases := []exp{
		{[]string{"", "A", "AB", "ABC", "R", "S"}, "A", []string{"", "R", "S"}, 3},
		{[]string{"", "A", "AB", "ABC", "R", "S"}, "ABC", []string{"", "A", "AB", "R", "S"}, 1},
		{[]string{"", "A", "AB", "ABC", "R", "S"}, "", []string{}, 6},
		{[]string{"", "A", "AB", "ABC", "R", "S"}, "S", []string{"", "A", "AB", "ABC", "R"}, 1},
		{[]string{"", "A", "AB", "ABC", "R", "S"}, "SS", []string{"", "A", "AB", "ABC", "R", "S"}, 0},
	}

	for _, test := range cases {
		r := New()
		for _, ss := range test.inp {
			r.Insert(ss, true)
		}

		deleted := r.DeletePrefix(test.prefix)
		if deleted != test.numDeleted {
			t.Fatalf("Bad delete, expected %v to be deleted but got %v", test.numDeleted, deleted)
		}

		out := []string{}
		fn := func(s string, v interface{}) bool {
			out = append(out, s)
			return false
		}
		r.Walk(fn)

		if !reflect.DeepEqual(out, test.out) {
			t.Fatalf("mis-match: %v %v", out, test.out)
		}
	}
}

func TestLongestPrefix(t *testing.T) {
	r := New()

	keys := []string{
		"",
		"foo",
		"foobar",
		"foobarbaz",
		"foobarbazzip",
		"foozip",
	}
	for _, k := range keys {
		r.Insert(k, nil)
	}
	if r.Len() != len(keys) {
		t.Fatalf("bad len: %v %v", r.Len(), len(keys))
	}

	type exp struct {
		inp string
		out string
	}
	cases := []exp{
		{"a", ""},
		{"abc", ""},
		{"fo", ""},
		{"foo", "foo"},
		{"foob", "foo"},
		{"foobar", "foobar"},
		{"foobarba", "foobar"},
		{"foobarbaz", "foobarbaz"},
		{"foobarbazzi", "foobarbaz"},
		{"foobarbazzip", "foobarbazzip"},
		{"foozi", "foo"},
		{"foozip", "foozip"},
		{"foozipzap", "foozip"},
	}
	for _, test := range cases {
		m, _, ok := r.LongestPrefix(test.inp)
		if !ok {
			t.Fatalf("no match: %v", test)
		}
		if m != test.out {
			t.Fatalf("mis-match: %v %v", m, test)
		}
	}
}

func TestWalkPrefix(t *testing.T) {
	r := New()

	keys := []string{
		"foobar",
		"foo/bar/baz",
		"foo/baz/bar",
		"foo/zip/zap",
		"zipzap",
	}
	for _, k := range keys {
		r.Insert(k, nil)
	}
	if r.Len() != len(keys) {
		t.Fatalf("bad len: %v %v", r.Len(), len(keys))
	}

	type exp struct {
		inp string
		out []string
	}
	cases := []exp{
		{
			"f",
			[]string{"foobar", "foo/bar/baz", "foo/baz/bar", "foo/zip/zap"},
		},
		{
			"foo",
			[]string{"foobar", "foo/bar/baz", "foo/baz/bar", "foo/zip/zap"},
		},
		{
			"foob",
			[]string{"foobar"},
		},
		{
			"foo/",
			[]string{"foo/bar/baz", "foo/baz/bar", "foo/zip/zap"},
		},
		{
			"foo/b",
			[]string{"foo/bar/baz", "foo/baz/bar"},
		},
		{
			"foo/ba",
			[]string{"foo/bar/baz", "foo/baz/bar"},
		},
		{
			"foo/bar",
			[]string{"foo/bar/baz"},
		},
		{
			"foo/bar/baz",
			[]string{"foo/bar/baz"},
		},
		{
			"foo/bar/bazoo",
			[]string{},
		},
		{
			"z",
			[]string{"zipzap"},
		},
	}

	for _, test := range cases {
		out := []string{}
		fn := func(s string, v interface{}) bool {
			out = append(out, s)
			return false
		}
		r.WalkPrefix(test.inp, fn)
		sort.Strings(out)
		sort.Strings(test.out)
		if !reflect.DeepEqual(out, test.out) {
			t.Fatalf("mis-match: %v %v", out, test.out)
		}
	}
}

func TestWalkPath(t *testing.T) {
	r := New()

	keys := []string{
		"foo",
		"foo/bar",
		"foo/bar/baz",
		"foo/baz/bar",
		"foo/zip/zap",
		"zipzap",
	}
	for _, k := range keys {
		r.Insert(k, nil)
	}
	if r.Len() != len(keys) {
		t.Fatalf("bad len: %v %v", r.Len(), len(keys))
	}

	type exp struct {
		inp string
		out []string
	}
	cases := []exp{
		{
			"f",
			[]string{},
		},
		{
			"foo",
			[]string{"foo"},
		},
		{
			"foo/",
			[]string{"foo"},
		},
		{
			"foo/ba",
			[]string{"foo"},
		},
		{
			"foo/bar",
			[]string{"foo", "foo/bar"},
		},
		{
			"foo/bar/baz",
			[]string{"foo", "foo/bar", "foo/bar/baz"},
		},
		{
			"foo/bar/bazoo",
			[]string{"foo", "foo/bar", "foo/bar/baz"},
		},
		{
			"z",
			[]string{},
		},
	}

	for _, test := range cases {
		out := []string{}
		fn := func(s string, v interface{}) bool {
			out = append(out, s)
			return false
		}
		r.WalkPath(test.inp, fn)
		sort.Strings(out)
		sort.Strings(test.out)
		if !reflect.DeepEqual(out, test.out) {
			t.Fatalf("mis-match: %v %v", out, test.out)
		}
	}
}

// generateUUID is used to generate a random UUID
func generateUUID() string {
	buf := make([]byte, 16)
	if _, err := crand.Read(buf); err != nil {
		panic(fmt.Errorf("failed to read random bytes: %v", err))
	}

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
		buf[0:4],
		buf[4:6],
		buf[6:8],
		buf[8:10],
		buf[10:16])
}

// TestWalkPostOrder verifies post-order traversal (children before parent, leaves first)
func TestWalkPostOrder(t *testing.T) {
	// Build a tree with known structure:
	//   root
	//   ├── a (leaf)
	//   ├── ab (leaf)
	//   ├── abc (leaf)
	//   └── b (leaf)
	// Post-order should visit leaves in depth-first order, children before ancestors
	tree := New()
	tree.Insert("a", 1)
	tree.Insert("ab", 2)
	tree.Insert("abc", 3)
	tree.Insert("b", 4)

	// Collect keys in post-order
	var postOrder []string
	tree.WalkPostOrder(func(k string, v interface{}) bool {
		postOrder = append(postOrder, k)
		return false
	})

	// Collect keys in pre-order (normal Walk)
	var preOrder []string
	tree.Walk(func(k string, v interface{}) bool {
		preOrder = append(preOrder, k)
		return false
	})

	// Pre-order visits parent before children: a, ab, abc, b
	// Post-order visits children before parent: abc, ab, a, b (deeper nodes first)
	expectedPreOrder := []string{"a", "ab", "abc", "b"}
	expectedPostOrder := []string{"abc", "ab", "a", "b"}

	if !reflect.DeepEqual(preOrder, expectedPreOrder) {
		t.Errorf("pre-order mismatch: got %v, want %v", preOrder, expectedPreOrder)
	}
	if !reflect.DeepEqual(postOrder, expectedPostOrder) {
		t.Errorf("post-order mismatch: got %v, want %v", postOrder, expectedPostOrder)
	}
}

// TestWalkPostOrder_DeepTree tests post-order with deeply nested structure
func TestWalkPostOrder_DeepTree(t *testing.T) {
	tree := New()
	// Create: root -> hello -> helloworld -> helloworldfoo
	tree.Insert("hello", 1)
	tree.Insert("helloworld", 2)
	tree.Insert("helloworldfoo", 3)
	tree.Insert("hi", 4)

	var postOrder []string
	tree.WalkPostOrder(func(k string, v interface{}) bool {
		postOrder = append(postOrder, k)
		return false
	})

	// Post-order: deepest first, then ancestors
	// helloworldfoo -> helloworld -> hello -> hi
	expected := []string{"helloworldfoo", "helloworld", "hello", "hi"}
	if !reflect.DeepEqual(postOrder, expected) {
		t.Errorf("post-order mismatch: got %v, want %v", postOrder, expected)
	}
}

// TestWalkPostOrder_EarlyTermination tests that post-order respects early termination
func TestWalkPostOrder_EarlyTermination(t *testing.T) {
	tree := New()
	tree.Insert("a", 1)
	tree.Insert("ab", 2)
	tree.Insert("abc", 3)
	tree.Insert("b", 4)

	var visited []string
	tree.WalkPostOrder(func(k string, v interface{}) bool {
		visited = append(visited, k)
		return k == "ab" // stop after visiting "ab"
	})

	// Should visit: abc, ab (then stop)
	expected := []string{"abc", "ab"}
	if !reflect.DeepEqual(visited, expected) {
		t.Errorf("early termination mismatch: got %v, want %v", visited, expected)
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	tree := New()

	// Test case 1: Tree has "helloworld", query with "hellobaby"
	tree.Insert("h", "value1")
	tree.Insert("he", "value1")
	tree.Insert("hi", "value1")
	tree.Insert("hei", "value1")
	tree.Insert("hell", "value1")
	tree.Insert("hellw", "value1")
	tree.Insert("x", "value1")
	tree.Insert("helloworld", "value1")

	key, val, length, _, found := tree.LongestCommonPrefix("hellobaby")
	if !found {
		t.Errorf("Expected to find a match for 'hellobaby'")
	}
	if key != "helloworld" {
		t.Errorf("Expected key 'helloworld', got '%s'", key)
	}
	if val != "value1" {
		t.Errorf("Expected value 'value1', got '%v'", val)
	}
	if length != 5 { // "hello" is the common prefix
		t.Errorf("Expected common prefix length 5, got %d", length)
	}

	// Test case 2: Tree has "helloworld", query with "hello"
	key, val, length, _, found = tree.LongestCommonPrefix("hello")
	if !found {
		t.Errorf("Expected to find a match for 'hello'")
	}
	if key != "helloworld" {
		t.Errorf("Expected key 'helloworld', got '%s'", key)
	}
	if length != 5 { // "hello" is the common prefix
		t.Errorf("Expected common prefix length 5, got %d", length)
	}

	// Test case 3: Add "hello", query with "helloworld" should match "helloworld" (longer match)
	tree.Insert("hello", "value2")
	key, val, length, _, found = tree.LongestCommonPrefix("helloworld")
	if !found {
		t.Errorf("Expected to find a match for 'helloworld'")
	}
	if key != "helloworld" { // Both "hello" and "helloworld" match, but "helloworld" has longer common prefix
		t.Errorf("Expected key 'helloworld', got '%s'", key)
	}
	if length != 10 { // "helloworld" is the common prefix
		t.Errorf("Expected common prefix length 10, got %d", length)
	}

	// Test case 4: Query with "hello" should match "helloworld" with length 5
	// (both "hello" and "helloworld" match, but we want the one with longest common prefix)
	key, val, length, _, found = tree.LongestCommonPrefix("hello")
	if !found {
		t.Errorf("Expected to find a match for 'hello'")
	}
	// Both "hello" (length 5) and "helloworld" (length 5) have the same common prefix length
	// The implementation will return one of them
	if length != 5 {
		t.Errorf("Expected common prefix length 5, got %d", length)
	}

	// Test case 5: No match
	tree2 := New()
	tree2.Insert("abc", "value3")
	key, val, length, _, found = tree2.LongestCommonPrefix("xyz")
	if found {
		t.Errorf("Expected no match for 'xyz', but found key '%s'", key)
	}
	if length != 0 {
		t.Errorf("Expected common prefix length 0, got %d", length)
	}

	// Test case 6: Partial match
	tree3 := New()
	tree3.Insert("helloworld", "value4")
	tree3.Insert("hellobaby", "value5")
	tree3.Insert("hi", "value6")

	key, val, length, _, found = tree3.LongestCommonPrefix("hellocat")
	if !found {
		t.Errorf("Expected to find a match for 'hellocat'")
	}
	// Should match either "helloworld" or "hellobaby" with length 5
	if length != 5 {
		t.Errorf("Expected common prefix length 5, got %d", length)
	}
	if key != "helloworld" && key != "hellobaby" {
		t.Errorf("Expected key 'helloworld' or 'hellobaby', got '%s'", key)
	}
}

func TestLongestCommonPrefixEdgeCases(t *testing.T) {
	// Test empty tree
	tree := New()
	key, _, length, _, found := tree.LongestCommonPrefix("test")
	if found {
		t.Errorf("Expected no match in empty tree, but found key '%s'", key)
	}
	if length != 0 {
		t.Errorf("Expected common prefix length 0 in empty tree, got %d", length)
	}

	// Test exact match
	tree.Insert("hello", "value1")
	key, val, length, _, found := tree.LongestCommonPrefix("hello")
	if !found {
		t.Errorf("Expected to find exact match for 'hello'")
	}
	if key != "hello" {
		t.Errorf("Expected key 'hello', got '%s'", key)
	}
	if val != "value1" {
		t.Errorf("Expected value 'value1', got '%v'", val)
	}
	if length != 5 {
		t.Errorf("Expected common prefix length 5, got %d", length)
	}

	// Test single character match
	tree2 := New()
	tree2.Insert("a", "value2")
	key, _, length, _, found = tree2.LongestCommonPrefix("abc")
	if !found {
		t.Errorf("Expected to find match for 'abc'")
	}
	if key != "a" {
		t.Errorf("Expected key 'a', got '%s'", key)
	}
	if length != 1 {
		t.Errorf("Expected common prefix length 1, got %d", length)
	}
}

// TestLongestCommonPrefixDeepTree tests LCP with deeply nested tree structure
func TestLongestCommonPrefixDeepTree(t *testing.T) {
	tree := New()

	// Create a deep tree: a -> ab -> abc -> abcd -> abcde -> abcdef
	tree.Insert("a", 1)
	tree.Insert("ab", 2)
	tree.Insert("abc", 3)
	tree.Insert("abcd", 4)
	tree.Insert("abcde", 5)
	tree.Insert("abcdef", 6)

	tests := []struct {
		input       string
		wantLen     int
		wantFound   bool
		description string
	}{
		{"abcdefg", 6, true, "input longer than deepest key"},
		{"abcdef", 6, true, "exact match with deepest key"},
		{"abcdex", 5, true, "diverge at last char"},
		{"abcxyz", 3, true, "diverge in middle"},
		{"axyz", 1, true, "diverge early"},
		{"xyz", 0, false, "no common prefix"},
		{"", 0, false, "empty input"},
	}

	for _, tt := range tests {
		_, _, length, _, found := tree.LongestCommonPrefix(tt.input)
		if found != tt.wantFound {
			t.Errorf("%s: found=%v, want %v", tt.description, found, tt.wantFound)
		}
		if length != tt.wantLen {
			t.Errorf("%s: length=%d, want %d", tt.description, length, tt.wantLen)
		}
	}
}

// TestLongestCommonPrefixWideBranching tests LCP with multiple sibling branches
func TestLongestCommonPrefixWideBranching(t *testing.T) {
	tree := New()

	// Create wide branching at "test" prefix
	// test -> testa, testb, testc, ... testz
	for c := 'a'; c <= 'z'; c++ {
		tree.Insert("test"+string(c)+"suffix", int(c))
	}

	tests := []struct {
		input       string
		wantLen     int
		wantFound   bool
		description string
	}{
		{"testasuffix", 11, true, "exact match first branch"},
		{"testzsuffix", 11, true, "exact match last branch"},
		{"testmprefix", 5, true, "partial match middle branch"},
		{"test", 4, true, "input is common prefix of all"},
		{"testxyz", 5, true, "diverge after branch char"},
		{"tes", 3, true, "input shorter than branch point"},
	}

	for _, tt := range tests {
		_, _, length, _, found := tree.LongestCommonPrefix(tt.input)
		if found != tt.wantFound {
			t.Errorf("%s: found=%v, want %v", tt.description, found, tt.wantFound)
		}
		if length != tt.wantLen {
			t.Errorf("%s: length=%d, want %d", tt.description, length, tt.wantLen)
		}
	}
}

// TestLongestCommonPrefixLongStrings tests LCP with long strings (LLM prompt scenario)
func TestLongestCommonPrefixLongStrings(t *testing.T) {
	tree := New()

	// Simulate LLM prompt caching scenario with long common prefixes
	basePrompt := "You are a helpful assistant. Please help me with the following task: "

	tree.Insert(basePrompt+"write code", "code")
	tree.Insert(basePrompt+"translate text", "translate")
	tree.Insert(basePrompt+"summarize article", "summarize")
	tree.Insert(basePrompt+"answer question", "answer")

	// Query with same base but different suffix
	key, val, length, _, found := tree.LongestCommonPrefix(basePrompt + "explain concept")
	if !found {
		t.Error("Expected to find match")
	}
	if length != len(basePrompt) {
		t.Errorf("Expected length %d, got %d", len(basePrompt), length)
	}
	_ = key
	_ = val

	// Query with exact match
	key, val, length, _, found = tree.LongestCommonPrefix(basePrompt + "write code")
	if !found {
		t.Error("Expected to find exact match")
	}
	if length != len(basePrompt)+10 {
		t.Errorf("Expected length %d, got %d", len(basePrompt)+10, length)
	}
	if val != "code" {
		t.Errorf("Expected value 'code', got %v", val)
	}
}

// TestLongestCommonPrefixSplitNode tests behavior when tree nodes split
func TestLongestCommonPrefixSplitNode(t *testing.T) {
	tree := New()

	// Insert in order that causes node splits
	tree.Insert("romane", 1)  // creates: root -> "romane"
	tree.Insert("romanus", 2) // splits: root -> "roman" -> "e", "us"
	tree.Insert("romulus", 3) // splits: root -> "rom" -> "an"->..., "ulus"
	tree.Insert("rubens", 4)  // creates: root -> "r" -> "om"->..., "ubens"
	tree.Insert("ruber", 5)   // splits: root -> "r" -> "om"->..., "ube" -> "ns", "r"
	tree.Insert("rubicon", 6) // splits: root -> "r" -> "om"->..., "ub" -> "e"->..., "icon"

	tests := []struct {
		input       string
		wantLen     int
		wantFound   bool
		description string
	}{
		{"romane", 6, true, "exact match romane"},
		{"roman", 5, true, "common prefix of romane/romanus"},
		{"roma", 4, true, "partial match"},
		{"rom", 3, true, "branch point prefix"},
		{"rubber", 3, true, "diverge at rub vs rubber"},
		{"rubicon", 7, true, "exact match rubicon"},
		{"rubicund", 5, true, "partial match rubic"},
		{"rx", 1, true, "diverge after r"},
		{"s", 0, false, "no match"},
	}

	for _, tt := range tests {
		_, _, length, _, found := tree.LongestCommonPrefix(tt.input)
		if found != tt.wantFound {
			t.Errorf("%s: found=%v, want %v", tt.description, found, tt.wantFound)
		}
		if length != tt.wantLen {
			t.Errorf("%s: length=%d, want %d", tt.description, length, tt.wantLen)
		}
	}
}

// TestLongestCommonPrefixSpecialCases tests special/boundary cases
func TestLongestCommonPrefixSpecialCases(t *testing.T) {
	// Case 1: Single character keys
	tree1 := New()
	tree1.Insert("a", 1)
	tree1.Insert("b", 2)
	tree1.Insert("c", 3)

	if _, _, length, _, found := tree1.LongestCommonPrefix("a"); !found || length != 1 {
		t.Errorf("single char exact match failed: found=%v, length=%d", found, length)
	}
	if _, _, length, _, found := tree1.LongestCommonPrefix("ab"); !found || length != 1 {
		t.Errorf("single char prefix match failed: found=%v, length=%d", found, length)
	}
	if _, _, _, _, found := tree1.LongestCommonPrefix("x"); found {
		t.Error("expected no match for 'x'")
	}

	// Case 2: Key is prefix of another key
	tree2 := New()
	tree2.Insert("pre", 1)
	tree2.Insert("prefix", 2)
	tree2.Insert("prefixation", 3)

	_, val, length, _, found := tree2.LongestCommonPrefix("prefix")
	if !found || length != 6 || val != 2 {
		t.Errorf("nested prefix exact match failed: found=%v, length=%d, val=%v", found, length, val)
	}

	_, _, length, _, found = tree2.LongestCommonPrefix("prefi")
	if !found || length != 5 {
		t.Errorf("nested prefix partial match failed: found=%v, length=%d", found, length)
	}

	// Case 3: Input matches multiple keys with same LCP length
	tree3 := New()
	tree3.Insert("testAAA", 1)
	tree3.Insert("testBBB", 2)
	tree3.Insert("testCCC", 3)

	_, _, length, _, found = tree3.LongestCommonPrefix("testXXX")
	if !found || length != 4 {
		t.Errorf("multiple same-length LCP failed: found=%v, length=%d", found, length)
	}

	// Case 4: Very short input
	tree4 := New()
	tree4.Insert("abcdefghij", 1)

	_, _, length, _, found = tree4.LongestCommonPrefix("a")
	if !found || length != 1 {
		t.Errorf("short input match failed: found=%v, length=%d", found, length)
	}

	_, _, length, _, found = tree4.LongestCommonPrefix("ab")
	if !found || length != 2 {
		t.Errorf("short input match failed: found=%v, length=%d", found, length)
	}
}

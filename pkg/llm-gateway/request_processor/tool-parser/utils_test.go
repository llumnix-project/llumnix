package tool_parser

import (
	"fmt"
	"testing"
)

func TestDynamicMatch(t *testing.T) {
	testCases := []struct {
		chunk  string
		target string
		result MatchResult
		pos    int
		desc   string
	}{
		// MatchAll - Complete suffix match
		{"abcd", "abcd", MatchAll, 0, "Complete suffix match"},
		{"xyzabcd", "abcd", MatchAll, 3, "Characters in the middle, complete suffix match"},
		{"1234567890abcd", "abcd", MatchAll, 10, "Long prefix, complete suffix match"},

		// MatchAll - Non-suffix complete match (should return NoMatch, because it's not a suffix)
		{"abcde", "abcd", NoMatch, -1, "Complete match but not suffix (characters after)"},
		{"xabcy", "abc", NoMatch, -1, "Complete match in the middle but not suffix"},
		{"abcdx", "abcd", NoMatch, -1, "Complete match at the beginning but not suffix"},

		// MatchPartial - Partial suffix match
		{"abc", "abcd", MatchPartial, 0, "Suffix is target prefix (short)"},
		{"xyzabc", "abcd", MatchPartial, 3, "Suffix is target prefix (with prefix)"},
		{"x", "xyz", MatchPartial, 0, "Single character suffix match"},
		{"aa", "aab", MatchPartial, 0, "Same prefix, different length"},
		{"hello wor", "world", MatchPartial, 6, "Real example: 'wor' is prefix of 'world'"},

		// NoMatch - Has prefix but not suffix
		{"abcx", "abcd", NoMatch, -1, "Has prefix but not suffix (characters after)"},
		{"xabcy", "abcd", NoMatch, -1, "Prefix in the middle but not suffix"},
		{"abx", "abcd", NoMatch, -1, "Prefix matches but not suffix"},

		// NoMatch - First character doesn't match
		{"bcd", "abcd", NoMatch, -1, "First character doesn't match"},
		{"xyz", "abc", NoMatch, -1, "Completely unrelated"},
		{"hello worldx", "world", NoMatch, -1, "Complete match but not suffix"},

		// Edge cases
		{"", "", MatchAll, 0, "Empty string match"},
		{"", "abc", NoMatch, -1, "Empty chunk, non-empty target"},
		{"abc", "", MatchAll, 0, "Non-empty chunk, empty target"},
		{"a", "a", MatchAll, 0, "Single character complete match"},
		{"a", "b", NoMatch, -1, "Single character doesn't match"},
		{"ab", "b", MatchAll, 1, "Single character suffix match"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			result, pos := dynamicMatch(tc.chunk, tc.target)
			if result != tc.result {
				t.Errorf("dynamicMatch(%q, %q) = %v, want %v",
					tc.chunk, tc.target, result, tc.result)
			}
			if pos != tc.pos {
				t.Errorf("dynamicMatch(%q, %q) position = %d, want %d",
					tc.chunk, tc.target, pos, tc.pos)
			}
		})
	}
}

func BenchmarkDynamicMatch(b *testing.B) {
	cases := []struct {
		chunk  string
		target string
	}{
		{"this is a very long chunk that contains the target at the end", "target"},
		{"short", "longer target string"},
		{"no match here at all", "completely different"},
		{"partial match but", "partial match but not complete"},
		{"exact match", "exact match"},
	}

	for _, c := range cases {
		b.Run(fmt.Sprintf("%s_%s", c.chunk[:min(10, len(c.chunk))], c.target[:min(10, len(c.target))]), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				dynamicMatch(c.chunk, c.target)
			}
		})
	}
}

func ExampleDynamicMatch() {
	examples := []struct {
		chunk  string
		target string
	}{
		{"hello world", "world"}, // MatchAll
		{"hello wor", "world"},   // MatchPartial
		{"hello x", "world"},     // NoMatch
		{"abcde", "abcd"},        // NoMatch (not suffix)
		{"xxxabcd", "abcd"},      // MatchAll
		{"xxxabc", "abcd"},       // MatchPartial
	}

	for _, ex := range examples {
		res, pos := dynamicMatch(ex.chunk, ex.target)
		resultStr := ""
		switch res {
		case MatchAll:
			resultStr = "MatchAll"
		case MatchPartial:
			resultStr = "MatchPartial"
		case NoMatch:
			resultStr = "NoMatch"
		}
		fmt.Printf("chunk=%q, target=%q -> %s, pos=%d\n",
			ex.chunk, ex.target, resultStr, pos)
	}

	// Output:
	// chunk="hello world", target="world" -> MatchAll, pos=6
	// chunk="hello wor", target="world" -> MatchPartial, pos=6
	// chunk="hello x", target="world" -> NoMatch, pos=-1
	// chunk="abcde", target="abcd" -> NoMatch, pos=-1
	// chunk="xxxabcd", target="abcd" -> MatchAll, pos=3
	// chunk="xxxabc", target="abcd" -> MatchPartial, pos=3
}

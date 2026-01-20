package gron

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode"
)

// A Token is a chunk of Text from a Statement with a type
type Token struct {
	Text string
	Typ  TokenTyp
}

// A TokenTyp identifies what kind of Token something is
type TokenTyp int

const (
	// A bare word is a unquoted key; like 'foo' in json.foo = 1;
	TypBare TokenTyp = iota

	// Numeric key; like '2' in json[2] = "foo";
	TypNumericKey

	// A quoted key; like 'foo bar' in json["foo bar"] = 2;
	TypQuotedKey

	// Punctuation types
	TypDot    // .
	TypLBrace // [
	TypRBrace // ]
	TypEquals // =
	TypSemi   // ;
	TypComma  // ,

	// Value types
	TypString      // "foo"
	TypNumber      // 4
	TypTrue        // true
	TypFalse       // false
	TypNull        // null
	TypEmptyArray  // []
	TypEmptyObject // {}

	// Ignored Token
	TypIgnored

	// Error Token
	TypError
)

// a sprintFn adds color to its input
type sprintFn func(...interface{}) string

// mapping of Token types to the appropriate color sprintFn
var sprintFns = map[TokenTyp]sprintFn{
	TypBare:        bareColor.SprintFunc(),
	TypNumericKey:  numColor.SprintFunc(),
	TypQuotedKey:   strColor.SprintFunc(),
	TypLBrace:      braceColor.SprintFunc(),
	TypRBrace:      braceColor.SprintFunc(),
	TypString:      strColor.SprintFunc(),
	TypNumber:      numColor.SprintFunc(),
	TypTrue:        boolColor.SprintFunc(),
	TypFalse:       boolColor.SprintFunc(),
	TypNull:        boolColor.SprintFunc(),
	TypEmptyArray:  braceColor.SprintFunc(),
	TypEmptyObject: braceColor.SprintFunc(),
}

// isValue returns true if the Token is a valid value type
func (t Token) isValue() bool {
	switch t.Typ {
	case TypString, TypNumber, TypTrue, TypFalse, TypNull, TypEmptyArray, TypEmptyObject:
		return true
	default:
		return false
	}
}

// isPunct returns true if the Token is a punctuation type
func (t Token) isPunct() bool {
	switch t.Typ {
	case TypDot, TypLBrace, TypRBrace, TypEquals, TypSemi, TypComma:
		return true
	default:
		return false
	}
}

// format returns the formatted version of the Token Text
func (t Token) format() string {
	if t.Typ == TypEquals {
		return " " + t.Text + " "
	}
	return t.Text
}

// formatColor returns the colored formatted version of the Token Text
func (t Token) formatColor() string {
	text := t.Text
	if t.Typ == TypEquals {
		text = " " + text + " "
	}
	fn, ok := sprintFns[t.Typ]
	if ok {
		return fn(text)
	}
	return text

}

// valueTokenFromInterface takes any valid value and
// returns a value Token to represent it
func valueTokenFromInterface(v interface{}) Token {
	switch vv := v.(type) {

	case map[string]interface{}:
		return Token{"{}", TypEmptyObject}
	case []interface{}:
		return Token{"[]", TypEmptyArray}
	case json.Number:
		return Token{vv.String(), TypNumber}
	case string:
		return Token{quoteString(vv), TypString}
	case bool:
		if vv {
			return Token{"true", TypTrue}
		}
		return Token{"false", TypFalse}
	case nil:
		return Token{"null", TypNull}
	default:
		return Token{"", TypError}
	}
}

// quoteString takes a string and returns a quoted and
// escaped string valid for use in gron output
func quoteString(s string) string {

	out := &bytes.Buffer{}
	// bytes.Buffer never returns errors on these methods.
	// errors are explicitly ignored to keep the linter
	// happy. A price worth paying so that the linter
	// remains useful.
	_ = out.WriteByte('"')

	for _, r := range s {
		switch r {
		case '\\':
			_, _ = out.WriteString(`\\`)
		case '"':
			_, _ = out.WriteString(`\"`)
		case '\b':
			_, _ = out.WriteString(`\b`)
		case '\f':
			_, _ = out.WriteString(`\f`)
		case '\n':
			_, _ = out.WriteString(`\n`)
		case '\r':
			_, _ = out.WriteString(`\r`)
		case '\t':
			_, _ = out.WriteString(`\t`)
		// \u2028 and \u2029 are separator runes that are not valid
		// in javascript strings so they must be escaped.
		// See http://timelessrepo.com/json-isnt-a-javascript-subset
		case '\u2028':
			_, _ = out.WriteString(`\u2028`)
		case '\u2029':
			_, _ = out.WriteString(`\u2029`)
		default:
			// Any other control runes must be escaped
			if unicode.IsControl(r) {
				_, _ = fmt.Fprintf(out, `\u%04X`, r)
			} else {
				// Unescaped rune
				_, _ = out.WriteRune(r)
			}
		}
	}

	_ = out.WriteByte('"')
	return out.String()

}

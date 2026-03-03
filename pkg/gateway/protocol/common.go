// openai protocol refer:  https://github.com/sashabaranov/go-openai/blob/master/chat.go

package protocol

import (
	"reflect"
	"sync"

	jsoniter "github.com/json-iterator/go"
)

type CompletionTokensDetails struct {
	ReasoningTokens uint64 `json:"reasoning_tokens"`
}
type PromptTokensDetails struct {
	CachedTokens uint64 `json:"cached_tokens"`
}

// common.go defines common types used throughout the OpenAI API.
// Usage Represents the total token usage per request to OpenAI.
type Usage struct {
	PromptTokens            uint64                   `json:"prompt_tokens"`
	CompletionTokens        uint64                   `json:"completion_tokens"`
	TotalTokens             uint64                   `json:"total_tokens"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
}

type ProtocolType int

func (p ProtocolType) String() string {
	switch p {
	case OpenAIChatCompletion:
		return "chat"
	case OpenAICompletion:
		return "completions"
	default:
		return "unknown"
	}
}

const (
	OpenAIChatCompletion ProtocolType = iota
	OpenAICompletion
)

// FieldSetter defines the interface for setting request fields.
// CompletionRequest and ChatCompletionRequest should implement this interface
// to support unified field assignment logic.
type FieldSetter interface {
	SetKvTransferParams(params map[string]interface{})
	SetRid(rid string)
	SetBootStrapHost(host string)
	SetBootStrapRoom(room int)
	SetMaxTokens(maxTokens int)
	SetStream(stream bool)
}

// ApplyRequestArgs applies key-value arguments to any request type that implements FieldSetter.
// This eliminates repetitive switch-case logic by using a dispatch table pattern.
// Example:
//
//	args := map[string]interface{}{"rid": "req-123", "max_tokens": 100}
//	ApplyRequestArgs(completionReq, args)
func ApplyRequestArgs[T FieldSetter](req T, args map[string]interface{}) {
	// Check if there are any arguments to process
	if len(args) == 0 {
		return
	}
	// Dispatch table: map field names to setter functions
	// This approach avoids deep switch nesting and improves readability.
	type fieldHandler func(T, interface{})
	dispatchTable := map[string]fieldHandler{
		"kv_transfer_params": func(r T, v interface{}) { r.SetKvTransferParams(v.(map[string]interface{})) },
		"rid":                func(r T, v interface{}) { r.SetRid(v.(string)) },
		"bootstrap_host":     func(r T, v interface{}) { r.SetBootStrapHost(v.(string)) },
		"bootstrap_room":     func(r T, v interface{}) { r.SetBootStrapRoom(v.(int)) },
		"max_tokens":         func(r T, v interface{}) { r.SetMaxTokens(v.(int)) },
		"stream":             func(r T, v interface{}) { r.SetStream(v.(bool)) },
	}

	for key, value := range args {
		if handler, exists := dispatchTable[key]; exists {
			handler(req, value)
		}
	}
}

// jsoniterAPI is the configured jsoniter instance for this package
var jsoniterAPI = jsoniter.ConfigCompatibleWithStandardLibrary

// fieldParser defines how to parse a specific field from JSON
type fieldParser struct {
	fieldIndex int                                              // Field index in struct
	parseFunc  func(iter *jsoniter.Iterator, val reflect.Value) // Custom parse function
}

// fieldMapWithExtra bundles field parsers with ExtraFields index for better cache efficiency
type fieldMapWithExtra struct {
	fieldMap       map[string]fieldParser // JSON field name -> parser
	extraFieldsIdx int                    // Index of ExtraFields field, -1 if not present
}

// structFieldMapCache caches field parsers and ExtraFields index for each struct type
// Key: reflect.Type, Value: fieldMapWithExtra
var structFieldMapCache sync.Map

// buildFieldMap creates a field parser map and finds ExtraFields index for the given struct type
// This function is called once per type and cached for performance
func buildFieldMap(typ reflect.Type) fieldMapWithExtra {
	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}

	fieldMap := make(map[string]fieldParser)
	extraFieldsIdx := -1

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		// Check if this is the ExtraFields field
		if field.Name == "ExtraFields" {
			extraFieldsIdx = i
			continue // Don't add ExtraFields to fieldMap
		}

		// Extract JSON tag
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		// Parse tag to get field name (ignore omitempty, etc.)
		jsonName := jsonTag
		for idx := 0; idx < len(jsonTag); idx++ {
			if jsonTag[idx] == ',' {
				jsonName = jsonTag[:idx]
				break
			}
		}

		// Create field parser based on field type
		parser := fieldParser{
			fieldIndex: i,
			parseFunc:  createParseFunc(field.Type),
		}

		fieldMap[jsonName] = parser
	}

	return fieldMapWithExtra{
		fieldMap:       fieldMap,
		extraFieldsIdx: extraFieldsIdx,
	}
}

// createParseFunc generates a parse function for the given field type
// This optimizes common types (string, int, bool, etc.) with direct reads
func createParseFunc(fieldType reflect.Type) func(iter *jsoniter.Iterator, val reflect.Value) {
	// Handle pointer types
	if fieldType.Kind() == reflect.Ptr {
		elemType := fieldType.Elem()
		switch elemType.Kind() {
		case reflect.Int:
			return func(iter *jsoniter.Iterator, val reflect.Value) {
				if iter.WhatIsNext() != jsoniter.NilValue {
					v := iter.ReadInt()
					val.Set(reflect.ValueOf(&v))
				} else {
					iter.ReadNil()
					val.Set(reflect.Zero(val.Type()))
				}
			}
		default:
			// Generic pointer handling
			return func(iter *jsoniter.Iterator, val reflect.Value) {
				if iter.WhatIsNext() != jsoniter.NilValue {
					newVal := reflect.New(elemType)
					iter.ReadVal(newVal.Interface())
					val.Set(newVal)
				} else {
					iter.ReadNil()
					val.Set(reflect.Zero(val.Type()))
				}
			}
		}
	}

	// Handle non-pointer types
	switch fieldType.Kind() {
	case reflect.String:
		return func(iter *jsoniter.Iterator, val reflect.Value) {
			val.SetString(iter.ReadString())
		}
	case reflect.Bool:
		return func(iter *jsoniter.Iterator, val reflect.Value) {
			val.SetBool(iter.ReadBool())
		}
	case reflect.Int:
		return func(iter *jsoniter.Iterator, val reflect.Value) {
			val.SetInt(int64(iter.ReadInt()))
		}
	case reflect.Float32:
		return func(iter *jsoniter.Iterator, val reflect.Value) {
			val.SetFloat(float64(iter.ReadFloat32()))
		}
	case reflect.Float64:
		return func(iter *jsoniter.Iterator, val reflect.Value) {
			val.SetFloat(iter.ReadFloat64())
		}
	default:
		// Generic handling for slices, maps, structs, interfaces, etc.
		return func(iter *jsoniter.Iterator, val reflect.Value) {
			// Create a pointer to the field for ReadVal
			ptr := val.Addr().Interface()
			iter.ReadVal(ptr)
		}
	}
}

// getOrBuildFieldMap retrieves cached field map with ExtraFields index or builds a new one
func getOrBuildFieldMap(typ reflect.Type) fieldMapWithExtra {
	if cached, ok := structFieldMapCache.Load(typ); ok {
		return cached.(fieldMapWithExtra)
	}

	// Build and cache
	fieldMapData := buildFieldMap(typ)
	structFieldMapCache.Store(typ, fieldMapData)
	return fieldMapData
}

// unmarshalWithReflection implements generic streaming unmarshal using reflection
// This function can be used by any struct with ExtraFields
func unmarshalWithReflection(data []byte, result interface{}) error {
	resultVal := reflect.ValueOf(result).Elem()
	resultType := resultVal.Type()

	// Get or build field parser map with cached ExtraFields index
	fieldMapData := getOrBuildFieldMap(resultType)

	// Borrow iterator from pool
	iter := jsoniterAPI.BorrowIterator(data)
	defer jsoniterAPI.ReturnIterator(iter)

	// Stream parse JSON object
	for field := iter.ReadObject(); field != ""; field = iter.ReadObject() {
		if parser, ok := fieldMapData.fieldMap[field]; ok {
			// Known field: use optimized parser
			fieldVal := resultVal.Field(parser.fieldIndex)
			parser.parseFunc(iter, fieldVal)
		} else {
			// Unknown field: store to ExtraFields
			if fieldMapData.extraFieldsIdx >= 0 {
				extraFields := resultVal.Field(fieldMapData.extraFieldsIdx)
				if extraFields.IsNil() {
					extraFields.Set(reflect.MakeMap(extraFields.Type()))
				}

				var value interface{}
				iter.ReadVal(&value)

				// Store unknown field
				extraFields.SetMapIndex(reflect.ValueOf(field), reflect.ValueOf(value))
			} else {
				// No ExtraFields, skip unknown field
				iter.Skip()
			}
		}
	}

	return iter.Error
}

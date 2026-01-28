package jsquery

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/go-playground/validator/v10"
	"k8s.io/klog/v2"

	jsonextension "llumnix/pkg/util/json"
	"llumnix/pkg/util/strcase"
)

var enableSuggestion = false

const (
	suggestionKey = "SUGGESTIONS"
	wildcard      = "*"
)

type jqErrors struct {
	errs []error
}

func (e *jqErrors) Error() string {
	sb := strings.Builder{}
	for i, err := range e.errs {
		sb.WriteString(err.Error())
		if i < len(e.errs)-1 {
			sb.WriteByte(',')
		}
	}
	return sb.String()
}

func SplitArgs(input, sep string, keepQuotes bool) ([]string, error) {
	separator := sep
	if sep == "" {
		separator = "\\s+"
	}
	singleQuoteOpen := false
	doubleQuoteOpen := false
	var tokenBuffer []string
	var ret []string

	arr := strings.Split(input, "")
	for _, element := range arr {
		matches, err := regexp.MatchString(separator, element)
		if err != nil {
			return nil, err
		}
		if element == "'" && !doubleQuoteOpen {
			if keepQuotes {
				tokenBuffer = append(tokenBuffer, element)
			}
			singleQuoteOpen = !singleQuoteOpen
			continue
		} else if element == `"` && !singleQuoteOpen {
			if keepQuotes {
				tokenBuffer = append(tokenBuffer, element)
			}
			doubleQuoteOpen = !doubleQuoteOpen
			continue
		}

		if !singleQuoteOpen && !doubleQuoteOpen && matches {
			if len(tokenBuffer) > 0 {
				ret = append(ret, strings.Join(tokenBuffer, ""))
				tokenBuffer = make([]string, 0)
			} else if sep != "" {
				ret = append(ret, element)
			}
		} else {
			tokenBuffer = append(tokenBuffer, element)
		}
	}
	if len(tokenBuffer) > 0 {
		ret = append(ret, strings.Join(tokenBuffer, ""))
	} else if sep != "" {
		ret = append(ret, "")
	}
	return ret, nil
}

func CountSeparators(input, separator string) (int, error) {
	if separator == "" {
		separator = "\\s+"
	}
	singleQuoteOpen := false
	doubleQuoteOpen := false
	ret := 0

	arr := strings.Split(input, "")
	for _, element := range arr {
		matches, err := regexp.MatchString(separator, element)
		if err != nil {
			return -1, err
		}
		if element == "'" && !doubleQuoteOpen {
			singleQuoteOpen = !singleQuoteOpen
			continue
		} else if element == `"` && !singleQuoteOpen {
			doubleQuoteOpen = !doubleQuoteOpen
			continue
		}

		if !singleQuoteOpen && !doubleQuoteOpen && matches {
			ret++
		}
	}
	return ret, nil
}

// jq (JSON Query) struct
type jq struct {
	data interface{}
}

func isArray(path string) bool {
	return len(path) >= 3 && strings.HasPrefix(path, "[") && strings.HasSuffix(path, "]")
}

func (q *jq) queryWildcardSliceIndex(path string, next string, context interface{}) ([]interface{}, error) {
	var errs []error
	if v, ok := context.([]interface{}); ok {
		ret := make([]interface{}, 0, len(v))
		for i := 0; i < len(v); i++ {
			d, err := (&jq{data: v[i]}).Query(next)
			if err != nil {
				errs = append(errs, err)
			} else {
				ret = append(ret, d)
			}
		}
		if len(errs) < len(v) {
			// TODO: should we return partial filled error, when
			// 	len(errs) > 0 ?
			return ret, nil
		} else {
			return nil, &jqErrors{errs: errs}
		}

	} else {
		return nil, errors.New(fmt.Sprint(path, " is not an array. ", v))
	}
}

// Query queries against the JSON with the expression passed in. The exp is separated by dots (".")
func (q *jq) Query(exp string) (interface{}, error) {
	if exp == "." {
		return q.data, nil
	}
	paths, err := SplitArgs(exp, "\\.", false)
	if err != nil {
		return nil, err
	}
	var context = q.data
	for i, path := range paths {
		if isArray(path) {
			indicator := path[1 : len(path)-1]
			// a.b.[*]
			if indicator == wildcard {
				next := strings.Join(paths[i+1:], ".")
				if len(next) == 0 {
					return context, err
				}
				return q.queryWildcardSliceIndex(path, next, context)
			}
			// array
			index, err := strconv.Atoi(indicator)
			if err != nil {
				return nil, err
			}
			if v, ok := context.([]interface{}); ok {
				if len(v) <= index {
					return nil, errors.New(fmt.Sprint(path, " index out of range."))
				}
				context = v[index]
			} else {
				return nil, errors.New(fmt.Sprint(path, " is not an array. ", v))
			}
		} else {
			// context not exist
			if context == nil {
				return nil, errors.New(fmt.Sprint(path, " does not exist."))
			}
			if v, ok := context.(map[string]interface{}); ok {
				// map
				if val, ok := queryMap(path, v); ok {
					context = val
				} else {
					return nil, errors.New(fmt.Sprint(path, " does not exist."))
				}
			} else if _, ok = context.([]interface{}); ok {
				return nil, errors.New(fmt.Sprintf("query specific key %s on an array is not supported", path))
			} else if vc := reflect.ValueOf(context); vc.Type().Kind() == reflect.Struct {
				// structured
				if f, ok := vc.Type().FieldByName(path); ok {
					context = vc.FieldByName(f.Name).Interface()
					continue
				} else {
					return nil, fmt.Errorf(path, " is not an object 2. ", v)
				}
			} else {
				return nil, fmt.Errorf("unable to query path %s on type %T", path, context)
			}
		}
	}
	return context, nil
}

func queryMap(key string, input map[string]interface{}) (interface{}, bool) {
	camelKey := strcase.ToLowerCamel(key)
	isCamel := camelKey == key
	if !isCamel {
		i, exist := input[key]
		if exist {
			return i, exist
		} else {
			i, exist = input[camelKey]
			return i, exist
		}
	} else {
		i, exist := input[camelKey]
		if exist {
			return i, exist
		} else {
			i, exist = input[strcase.ToSnakeWithIgnore(camelKey, ".")]
			return i, exist
		}
	}
}

func (q *jq) QueryWithDefault(exp string, defaultValue interface{}) interface{} {
	if retval, err := q.Query(exp); err == nil {
		return retval
	} else {
		return defaultValue
	}
}

// QueryToMap queries against the JSON with the expression passed in, and convert to a map[string]interface{}
func (q *jq) QueryToMap(exp string) (map[string]interface{}, error) {
	r, err := q.Query(exp)
	if err != nil {
		return nil, errors.New("Failed to parse: " + exp)
	}
	if ret, ok := r.(map[string]interface{}); ok {
		return ret, nil
	}
	return nil, errors.New("Failed to convert to map: " + exp)
}

// QueryToArray queries against the JSON with the expression passed in, and convert to a array: []interface{}
func (q *jq) QueryToArray(exp string) ([]interface{}, error) {
	r, err := q.Query(exp)
	if err != nil {
		return nil, errors.New("Failed to parse: " + exp)
	}
	if ret, ok := r.([]interface{}); ok {
		return ret, nil
	}
	return nil, errors.New("Failed to convert to array: " + exp)
}

// QueryToJq queries against the JSON with the expression passed in, and convert to a new JQ struct
func (q *jq) QueryToJq(exp string) (*jq, error) {
	r, err := q.Query(exp)
	if err != nil {
		return nil, errors.New("Failed to parse: " + exp)
	}
	return &jq{data: r}, nil
}

// QueryToMap queries against the JSON with the expression passed in, and convert to string
func (q *jq) QueryToString(exp string) (string, error) {
	r, err := q.Query(exp)
	if err != nil {
		return "", errors.New("Failed to parse: " + exp)
	}
	//switch v := r.(type) {
	switch r.(type) {
	case string:
		if ret, ok := r.(string); ok {
			return ret, nil
		}
	case float64:
		if ret, ok := r.(float64); ok {
			return fmt.Sprintf("%v", ret), nil
		}
	default:
		//fmt.Printf("key [%v] has type %v, converted to string", exp, v)
		byte := bytes.NewBuffer([]byte{})
		jsonEncoder := json.NewEncoder(byte)
		jsonEncoder.SetEscapeHTML(false)
		jsonEncoder.Encode(r)
		return byte.String(), nil
	}
	return "", errors.New("Failed to convert to string: " + exp)
}

// QueryToMap queries against the JSON with the expression passed in, and convert to int64
func (q *jq) QueryToInt64(exp string) (int64, error) {
	r, err := q.Query(exp)
	if err != nil {
		return 0, errors.New("Failed to parse: " + exp)
	}
	switch v := r.(type) {
	case float64:
		if ret, ok := r.(float64); ok {
			return int64(ret), nil
		}
	case string:
		if ret, ok := r.(string); ok {
			num, err := strconv.Atoi(ret)
			if err != nil {
				return 0, err
			}
			return int64(num), nil
		}
	case int64:
		if ret, ok := r.(int64); ok {
			return ret, nil
		}
	case int:
		if ret, ok := r.(int); ok {
			return int64(ret), nil
		}
	default:
		return 0, fmt.Errorf("Failed to convert to int64: %v", v)

	}
	return 0, errors.New("Failed to convert to int64: " + exp)
}

func (q *jq) QueryToInt(exp string) (int, error) {
	num, err := q.QueryToInt64(exp)
	return int(num), err
}

// QueryToMap queries against the JSON with the expression passed in, and convert to float64
func (q *jq) QueryToFloat64(exp string) (float64, error) {
	r, err := q.Query(exp)
	if err != nil {
		return 0, errors.New("Failed to parse: " + exp)
	}
	if ret, ok := r.(float64); ok {
		return ret, nil
	}
	return 0, errors.New("Failed to convert to float64: " + exp)
}

// QueryToMap queries against the JSON with the expression passed in, and convert to bool
func (q *jq) QueryToBool(exp string) (bool, error) {
	r, err := q.Query(exp)
	if err != nil {
		return false, errors.New("Failed to parse: " + exp)
	}

	switch v := r.(type) {
	case string:
		if ret, ok := r.(string); ok {
			return strconv.ParseBool(ret)
		}
	case bool:
		if ret, ok := r.(bool); ok {
			return ret, nil
		}
	default:
		return false, fmt.Errorf("Failed to convert to bool: %v", v)
	}

	return false, errors.New("Failed to convert to bool: " + exp)
}

// Set sets against the JSON with the expression passed in. The exp is separated by dots (".")
func (q *jq) Set(exp string, value interface{}) error {
	if exp == "." {
		return errors.New("exp [.] is not allowed to be set")
	}
	paths, err := SplitArgs(exp, "\\.", false)
	if err != nil {
		return err
	}
	if value != nil {
		if vc := reflect.ValueOf(value); vc.Type().Kind() == reflect.Struct {
			// if struct has a json tag different from field name. list keys will occur error.
			// convert to map[string]interface to avoid this error.
			marshal, err := json.Marshal(value)
			if err != nil {
				return err
			}
			value = map[string]interface{}{}
			err = json.Unmarshal(marshal, &value)
			if err != nil {
				return err
			}
		}
	}

	var prevContext interface{}
	var prevPath string
	var context = q.data
	for idx, path := range paths {
		if isArray(path) {
			index, err := strconv.Atoi(path[1 : len(path)-1])
			if err != nil {
				return err
			}

			if v, ok := context.([]interface{}); ok {
				if len(v) <= index {
					if prevContext != nil && len(prevPath) > 0 {
						prevMap, ok := prevContext.(map[string]interface{})
						if ok {
							// expand the slice only
							slice := make([]interface{}, index+1)
							copy(slice, v)
							prevMap[prevPath] = slice
							v = slice
						} else {
							return errors.New("cannot expand the slice")
						}
					} else {
						return errors.New("slice is short")
					}
				}
				if idx == len(paths)-1 {
					// Check type validation
					if v[index] != nil && reflect.TypeOf(value) != reflect.TypeOf(v[index]) {
						return errors.New(fmt.Sprintf("type of value and path mismatch [%v, %v]",
							reflect.TypeOf(value), reflect.TypeOf(v[index])))
					} else {
						v[index] = value
						return nil
					}
				}
				prevContext = context
				context = v[index]
			} else if m, ok := context.(map[string]interface{}); ok && prevContext != nil && len(m) == 0 {
				// rewrite the map to array
				if prevMap, ok := prevContext.(map[string]interface{}); ok {
					slice := make([]interface{}, index+1)
					prevMap[prevPath] = slice
					slice[index] = value
					prevContext = slice
					context = slice[index]
				}

			} else {
				return errors.New(fmt.Sprint(path, " is not an array. ", v))
			}
		} else {
			if v, ok := context.(map[string]interface{}); ok {
				if idx == len(paths)-1 {
					v[path] = value
					return nil
				}
				if val, ok := v[path]; ok {
					prevContext = context
					context = val
				} else {
					v[path] = make(map[string]interface{})
					prevContext = context
					context, _ = v[path]
				}
			} else {
				return errors.New(fmt.Sprint(path, " is not an object. ", v))
			}
		}
		prevPath = path
	}
	return nil
}

// Set sets against the JSON with the expression passed in. The exp is separated by dots (".")
func (q *jq) Remove(exp string) error {
	if exp == "." {
		return errors.New("exp [.] is not allowed to be set")
	}
	paths, err := SplitArgs(exp, "\\.", false)
	if err != nil {
		return err
	}

	var context = q.data
	for idx, path := range paths {
		if len(path) == 0 {
			return fmt.Errorf("unexpected empty path, exp: %s", exp)
		}

		if isArray(path) {
			index, err := strconv.Atoi(path[1 : len(path)-1])
			if err != nil {
				return err
			}

			if v, ok := context.([]interface{}); ok {
				if len(v) <= index {
					return errors.New(fmt.Sprint(path, " index out of range."))
				}
				context = v[index]
			} else {
				return errors.New(fmt.Sprint(path, " is not an array. ", v))
			}
		} else {
			if v, ok := context.(map[string]interface{}); ok {
				if idx == len(paths)-1 {
					delete(v, path)
					if len(v) == 0 {
						return q.Remove(strings.Join(paths[:idx], "."))
					}
					return nil
				}
				if val, ok := v[path]; ok {
					context = val
				} else {
					v[path] = make(map[string]interface{})
					context, _ = v[path]
				}
			} else {
				return errors.New(fmt.Sprint(path, " is not an object. ", v))
			}
		}
	}
	return nil
}

// ListKeys list the dotted prefixes of the input json
func (q *jq) ListKeys() ([]string, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(q.String()), &data); err != nil {
		return nil, err
	}

	var looper func(data map[string]interface{}, longest string, keySet *[]string)
	looper = func(data map[string]interface{}, longest string, keySet *[]string) {
		for key, val := range data {
			thisKey := longest + "." + key
			if pos, ok := val.(map[string]interface{}); ok {
				looper(pos, thisKey, keySet)
			} else {
				*keySet = append(*keySet, thisKey)
			}
		}
	}

	keySet := make([]string, 0)

	for key, val := range data {
		if pos, ok := val.(map[string]interface{}); ok {
			looper(pos, key, &keySet)
		} else {
			keySet = append(keySet, key)
		}
	}

	return keySet, nil
}

func (q *jq) ListKeysCascade() ([]string, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(q.String()), &data); err != nil {
		return nil, err
	}

	var mapLooper func(data map[string]interface{}, longest string, keySet *[]string)
	var listLooper func(data []interface{}, longest string, keySet *[]string)
	mapLooper = func(data map[string]interface{}, longest string, keySet *[]string) {
		if len(data) == 0 {
			*keySet = append(*keySet, longest)
			return
		}
		for key, val := range data {
			thisKey := longest + "." + key
			if pos, ok := val.(map[string]interface{}); ok {
				if len(pos) == 0 {
					*keySet = append(*keySet, thisKey)
				} else {
					mapLooper(pos, thisKey, keySet)
				}
			} else if pos1, ok1 := val.([]interface{}); ok1 {
				listLooper(pos1, thisKey, keySet)
			} else {
				*keySet = append(*keySet, thisKey)
			}
		}
	}
	listLooper = func(data []interface{}, longest string, keySet *[]string) {
		if len(data) == 0 {
			*keySet = append(*keySet, longest)
			return
		}
		for key, val := range data {
			thisKey := longest + fmt.Sprintf(".[%v]", key)
			if pos, ok := val.(map[string]interface{}); ok {
				mapLooper(pos, thisKey, keySet)
			} else if pos1, ok1 := val.([]interface{}); ok1 {
				listLooper(pos1, thisKey, keySet)
			} else {
				*keySet = append(*keySet, thisKey)
			}
		}
	}

	keySet := make([]string, 0)

	for key, val := range data {
		if pos, ok := val.(map[string]interface{}); ok {
			mapLooper(pos, key, &keySet)
		} else if pos1, ok1 := val.([]interface{}); ok1 {
			listLooper(pos1, key, &keySet)
		} else {
			keySet = append(keySet, key)
		}
	}

	return keySet, nil
}

// Has test whether the given expression is exists in JSON
func (q *jq) Has(exp string) bool {
	_, err := q.Query(exp)
	return err == nil
}

// String return the full JSON in compacted string
func (q *jq) String() string {
	byte := bytes.NewBuffer([]byte{})
	jsonEncoder := json.NewEncoder(byte)
	jsonEncoder.SetEscapeHTML(false)
	jsonEncoder.SetIndent("", "")
	jsonEncoder.Encode(q.data)
	return byte.String()
}

func (q *jq) Bytes() []byte {
	byte := bytes.NewBuffer([]byte{})
	jsonEncoder := json.NewEncoder(byte)
	jsonEncoder.SetEscapeHTML(false)
	jsonEncoder.SetIndent("", "")
	jsonEncoder.Encode(q.data)
	return byte.Bytes()
}

// String return the full styled JSON in string
func (q *jq) StyledString() string {
	byte := bytes.NewBuffer([]byte{})
	jsonEncoder := json.NewEncoder(byte)
	jsonEncoder.SetEscapeHTML(false)
	jsonEncoder.SetIndent("", "  ")
	jsonEncoder.Encode(q.data)
	return byte.String()
}

func (q *jq) As(out interface{}) error {
	return jsonextension.
		DefaultExtendedJSON.
		WithAutoTypeConverting(true).
		Unmarshal(q.Bytes(), out)
}

// IsTrue test whether the given expression is True (also support string "true")
func (q *jq) IsTrue(exp string) bool {
	ok, err := q.QueryToBool(exp)
	if err != nil {
		okStr, err := q.QueryToString(exp)
		if err == nil {
			okOut, err := strconv.ParseBool(okStr)
			if err == nil {
				ok = okOut
			}
		}
	}
	return ok
}

type EquivalenceKey struct {
	Keys      []string
	Preferred string
	hint      string
	options   queryOptions
	exactKey  string
}

func (e *EquivalenceKey) Format() string {
	return strings.Join(append([]string{e.Preferred}, e.Keys...), ",")
}

func (e *EquivalenceKey) String() string {
	return fmt.Sprintf("preferred: %s, compatible: %v", e.Preferred, e.Keys)
}

func (e *EquivalenceKey) AddSub(key string) *EquivalenceKey {
	ret := e.Clone()
	toAdd := "." + key
	ret.Preferred += toAdd
	for i := range e.Keys {
		ret.Keys[i] += toAdd
	}
	return ret
}

func (e *EquivalenceKey) Clone() *EquivalenceKey {
	keys := make([]string, len(e.Keys))
	copy(keys, e.Keys)
	return &EquivalenceKey{
		Keys:      keys,
		Preferred: e.Preferred,
		hint:      e.hint,
		options:   e.options,
		exactKey:  e.exactKey,
	}
}

type EquivalentQuery struct {
	*jq
	noExportPath map[string]bool
}

type queryOptions struct {
	noPreferred     bool
	removeRedundant bool
	hideOnExport    bool
	addSuggestion   bool
	caseCompatible  bool
	cascade         bool
	suggestionKey   string
}

func defaultQueryOptions() queryOptions {
	return queryOptions{
		removeRedundant: true,
		addSuggestion:   false,
		caseCompatible:  true,
		suggestionKey:   suggestionKey,
	}
}

type Option func(options *queryOptions)

func RemoveRedundant() Option {
	return func(options *queryOptions) {
		options.removeRedundant = true
	}
}

func ExactlyQuery() Option {
	return func(options *queryOptions) {
		options.caseCompatible = false
	}
}

func CompatibleQuery() Option {
	return func(options *queryOptions) {
		options.caseCompatible = true
	}
}

func Cascade() Option {
	return func(options *queryOptions) {
		options.cascade = true
	}
}

func NoPreferred() Option {
	return func(options *queryOptions) {
		options.noPreferred = true
	}
}

// Query queries against the JSON with the expression passed in. The exp is separated by dots (".")
func (q *EquivalentQuery) Query(exp *EquivalenceKey, options ...Option) (interface{}, error) {
	opt := exp.options
	for _, fn := range options {
		fn(&opt)
	}
	if opt.caseCompatible {
		if strcase.ToSnake(exp.Preferred) == exp.Preferred {
			exp.Keys = append(exp.Keys, strcase.ToCamel(exp.Preferred))
		} else if strcase.ToCamel(exp.Preferred) == exp.Preferred {
			exp.Keys = append(exp.Keys, strcase.ToSnake(exp.Preferred))
		}
	}
	i, err := q.jq.Query(exp.Preferred)
	if err == nil {
		if opt.hideOnExport {
			q.noExportPath[exp.Preferred] = true
		}
		return i, nil
	} else /* preferred key not exist */ {
		for _, key := range exp.Keys {
			i, err = q.jq.Query(key)
			if err == nil /* key exist */ {
				var e1 error
				if !opt.noPreferred {
					/* set the preferred key value */
					e1 = q.jq.Set(exp.Preferred, i)
					if e1 != nil {
						return nil, fmt.Errorf("unable set key %s, error: %v", exp.Preferred, err)
					}
				} else /* the specific key found, and ignore preferred key */ {
					if opt.hideOnExport {
						if !opt.removeRedundant {
							q.noExportPath[key] = true
						}
					}
					if opt.removeRedundant {
						redundant := exp.Keys
						for _, key2 := range redundant {
							if key2 == key {
								continue
							} else {
								q.jq.Remove(key2)
							}
						}
					}
					return i, err
				}
				if opt.addSuggestion {
					sgst, _ := q.jq.QueryToString(opt.suggestionKey)
					if len(sgst) > 0 {
						sgst += ","
					}
					sgst += fmt.Sprintf("migrate key '%s' to '%s'", key, exp.Preferred)
					if len(exp.hint) > 0 {
						sgst += " for " + exp.hint
					}
					err = q.jq.Set(opt.suggestionKey, sgst)
					if err != nil {
						return nil, fmt.Errorf("unable to set suggestion, error: %v", err)
					}
				}
				if opt.removeRedundant && !opt.noPreferred {
					/* remove the key since preferred key already setup */
					e1 = q.jq.Remove(key)
					if e1 != nil {
						return nil, fmt.Errorf("unable remove key %s, error: %v", exp.Preferred, err)
					}
				}
				if opt.hideOnExport {
					if !opt.noPreferred {
						q.noExportPath[exp.Preferred] = true
					}
					if !opt.removeRedundant {
						q.noExportPath[key] = true
					}
				}
				return i, err
			}
		}
		return i, err
	}
}

func (q *EquivalentQuery) Export() (string, error) {
	var data = new(interface{})
	err := json.Unmarshal([]byte(q.jq.String()), data)
	if err != nil {
		return "", err
	}
	j := &jq{data: *data}
	for key := range q.noExportPath {
		j.Remove(key)
	}
	return j.String(), nil
}

// Query queries against the JSON with the expression passed in. The exp is separated by dots (".")
func (q *EquivalentQuery) Set(exp EquivalenceKey, value interface{}) error {
	if err := q.jq.Set(exp.Preferred, value); err != nil {
		return err
	}
	if exp.options.hideOnExport {
		q.noExportPath[exp.Preferred] = true
	}

	for _, key := range exp.Keys {
		q.jq.Remove(key)
	}
	return nil
}

func (q *EquivalentQuery) Remove(exp EquivalenceKey) error {
	var ret error
	for _, key := range append(exp.Keys, exp.Preferred) {
		if err := q.jq.Remove(key); err != nil {
			ret = err
		}
	}
	return ret
}

func (q *EquivalentQuery) QueryWithDefault(exp *EquivalenceKey, defaultValue interface{}) interface{} {
	if retval, err := q.Query(exp); err == nil {
		return retval
	} else {
		return defaultValue
	}
}

// QueryToMap queries against the JSON with the expression passed in, and convert to a map[string]interface{}
func (q *EquivalentQuery) QueryToMap(exp *EquivalenceKey, options ...Option) (map[string]interface{}, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return nil, errors.New("Failed to parse: " + exp.String())
	}
	if ret, ok := r.(map[string]interface{}); ok {
		return ret, nil
	}
	return nil, errors.New("Failed to convert to map: " + exp.String())
}

// QueryToArray queries against the JSON with the expression passed in, and convert to a array: []interface{}
func (q *EquivalentQuery) QueryToArray(exp *EquivalenceKey, options ...Option) ([]interface{}, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return nil, errors.New("Failed to parse: " + exp.String())
	}
	if ret, ok := r.([]interface{}); ok {
		return ret, nil
	}
	return nil, errors.New("Failed to convert to array: " + exp.String())
}

func (q *EquivalentQuery) QueryToString(exp *EquivalenceKey, options ...Option) (string, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return "", errors.New("Failed to parse: " + exp.String())
	}
	//switch v := r.(type) {
	switch r.(type) {
	case string:
		if ret, ok := r.(string); ok {
			return ret, nil
		}
	case float64:
		if ret, ok := r.(float64); ok {
			return fmt.Sprintf("%v", ret), nil
		}
	default:
		klog.Infof("key [%v] has type %T, value %v converted to string", exp, r, r)
		return fmt.Sprintf("%v", r), nil
	}
	return "", errors.New("Failed to convert to string: " + exp.String())
}

func (q *EquivalentQuery) QueryToInt64(exp *EquivalenceKey, options ...Option) (int64, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return 0, errors.New("Failed to parse: " + exp.String())
	}
	switch v := r.(type) {
	case float64:
		if ret, ok := r.(float64); ok {
			return int64(ret), nil
		}
	case string:
		if ret, ok := r.(string); ok {
			num, err := strconv.Atoi(ret)
			if err != nil {
				return 0, err
			}
			return int64(num), nil
		}
	case int64:
		if ret, ok := r.(int64); ok {
			return ret, nil
		}
	case int:
		if ret, ok := r.(int); ok {
			return int64(ret), nil
		}
	default:
		return 0, fmt.Errorf("Failed to convert to int64: %v", v)

	}
	return 0, errors.New("Failed to convert to int64: " + exp.String())
}

func (q *EquivalentQuery) QueryToInt(exp *EquivalenceKey, options ...Option) (interface{}, error) {
	num, err := q.QueryToInt64(exp, options...)
	return int(num), err
}

func (q *EquivalentQuery) QueryToFloat64(exp *EquivalenceKey, options ...Option) (float64, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return 0, errors.New("Failed to parse: " + exp.String())
	}
	if ret, ok := r.(float64); ok {
		return ret, nil
	}
	return 0, errors.New("Failed to convert to float64: " + exp.String())
}

// QueryToBool queries against the JSON with the expression passed in, and convert to bool
func (q *EquivalentQuery) QueryToBool(exp *EquivalenceKey, options ...Option) (bool, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return false, errors.New("Failed to parse: " + exp.String())
	}

	switch v := r.(type) {
	case string:
		if ret, ok := r.(string); ok {
			return strconv.ParseBool(ret)
		}
	case bool:
		if ret, ok := r.(bool); ok {
			return ret, nil
		}
	default:
		return false, fmt.Errorf("Failed to convert to bool: %v", v)
	}

	return false, errors.New("Failed to convert to bool: " + exp.String())
}

// Has test whether the given expression exists in JSON
func (q *EquivalentQuery) Has(exp *EquivalenceKey, options ...Option) bool {
	_, err := q.Query(exp, options...)
	return err == nil
}

// IsTrue test whether the given expression is True (also support string "true")
func (q *EquivalentQuery) IsTrue(exp *EquivalenceKey, options ...Option) bool {
	ok, err := q.QueryToBool(exp, options...)
	if err != nil {
		okStr, err := q.QueryToString(exp, options...)
		if err == nil {
			okOut, err := strconv.ParseBool(okStr)
			if err == nil {
				ok = okOut
			}
		}
	}
	return ok
}

type JQ struct {
	e        *EquivalentQuery
	lastExp  string
	hideKeys []string
}

func NewEJQ(jq *jq) *JQ {
	ret := &JQ{e: &EquivalentQuery{jq, map[string]bool{}}}
	if ret.Has(suggestionKey) {
		ret.Remove(suggestionKey)
	}

	return ret
}

func (q *JQ) DeepCopy() *JQ {
	js, _ := NewStringQuery(q.String())
	return js
}

func (q *JQ) Clone() *JQ {
	ret := &JQ{
		hideKeys: make([]string, len(q.hideKeys)),
		e: &EquivalentQuery{
			jq:           q.e.jq,
			noExportPath: map[string]bool{},
		},
	}
	copy(ret.hideKeys, q.hideKeys)
	for key, val := range q.e.noExportPath {
		ret.e.noExportPath[key] = val
	}
	return ret
}

func (q *JQ) parseKey(input string) EquivalenceKey {
	return ParseKey(input)
}

// Query queries against the JSON with the expression passed in. The exp is separated by dots (".")
func (q *JQ) Query(query string, options ...Option) (interface{}, error) {
	ek := q.parseKey(query)
	i, err := q.e.Query(&ek, options...)
	if err == nil {
		q.lastExp = query
	}
	return i, err
}

func (q *JQ) QueryWithDefault(query string, defaultValue interface{}, options ...Option) interface{} {
	if retval, err := q.Query(query, options...); err == nil {
		return retval
	} else {
		return defaultValue
	}
}

// QueryToMap queries against the JSON with the expression passed in, and convert to a map[string]interface{}
func (q *JQ) QueryToMap(exp string, options ...Option) (map[string]interface{}, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return nil, errors.New("Failed to parse: " + exp)
	}
	if ret, ok := r.(map[string]interface{}); ok {
		return ret, nil
	}
	return nil, errors.New("Failed to convert to map: " + exp)
}

// QueryToArray queries against the JSON with the expression passed in, and convert to a array: []interface{}
func (q *JQ) QueryToArray(exp string, options ...Option) ([]interface{}, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return nil, errors.New("Failed to parse: " + exp)
	}
	if ret, ok := r.([]interface{}); ok {
		return ret, nil
	}
	return nil, errors.New("Failed to convert to array: " + exp)
}

func (q *JQ) QueryToString(exp string, options ...Option) (string, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return "", errors.New("Failed to parse: " + exp)
	}
	//switch v := r.(type) {
	switch r.(type) {
	case string:
		if ret, ok := r.(string); ok {
			return ret, nil
		}
	case float64:
		if ret, ok := r.(float64); ok {
			return strconv.FormatFloat(ret, 'f', -1, 64), nil
		}
	case float32:
		if ret, ok := r.(float32); ok {
			return strconv.FormatFloat(float64(ret), 'f', -1, 32), nil
		}
	case int:
		if ret, ok := r.(int); ok {
			return strconv.Itoa(ret), nil
		}
	case int8:
		if ret, ok := r.(int8); ok {
			return strconv.Itoa(int(ret)), nil
		}
	case int16:
		if ret, ok := r.(int16); ok {
			return strconv.Itoa(int(ret)), nil
		}
	case int32:
		if ret, ok := r.(int32); ok {
			return strconv.Itoa(int(ret)), nil
		}
	case int64:
		if ret, ok := r.(int64); ok {
			return strconv.FormatInt(ret, 10), nil
		}
	case uint:
		if ret, ok := r.(uint); ok {
			return strconv.FormatUint(uint64(ret), 10), nil
		}
	case uint8:
		if ret, ok := r.(uint8); ok {
			return strconv.FormatUint(uint64(ret), 10), nil
		}
	case uint16:
		if ret, ok := r.(uint16); ok {
			return strconv.FormatUint(uint64(ret), 10), nil
		}
	case uint32:
		if ret, ok := r.(uint32); ok {
			return strconv.FormatUint(uint64(ret), 10), nil
		}
	case uint64:
		if ret, ok := r.(uint64); ok {
			return strconv.FormatUint(ret, 10), nil
		}
	case bool:
		if ret, ok := r.(bool); ok {
			return strconv.FormatBool(ret), nil
		}
	case nil:
		return "", nil
	default:
		data := bytes.NewBuffer([]byte{})
		jsonEncoder := json.NewEncoder(data)
		jsonEncoder.SetEscapeHTML(false)
		jsonEncoder.Encode(r)
		klog.Warningf("key [%v] has type %T, value %v converted to string %s", exp, r, r, data.String())
		return data.String(), nil
	}
	return "", errors.New("Failed to convert to string: " + exp)
}

func (q *JQ) QueryToStringWithDefault(exp string, def string, options ...Option) string {
	s, err := q.QueryToString(exp, options...)
	if err != nil {
		return def
	}
	return s
}

// Set sets against the JSON with the expression passed in. The exp is separated by dots (".")
func (q *JQ) Set(exp string, value interface{}) error {
	return q.e.Set(q.parseKey(exp), value)
}

// QueryToJq queries against the JSON with the expression passed in, and convert to a new JQ struct
func (q *JQ) QueryToJq(exp string, options ...Option) (*JQ, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return nil, errors.New("Failed to parse: " + exp)
	}
	return NewQueryWithExp(r, exp), nil
}

func (q *JQ) QueryToIntOrString(exp string, options ...Option) (intstr.IntOrString, error) {
	str, err := q.QueryToString(exp, options...)
	if err != nil {
		return intstr.IntOrString{}, err
	}
	return intstr.Parse(str), nil
}

func (q *JQ) QueryToInt64(exp string, options ...Option) (int64, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return 0, errors.New("Failed to parse: " + exp)
	}
	switch v := r.(type) {
	case float64:
		if ret, ok := r.(float64); ok {
			return int64(ret), nil
		}
	case string:
		if ret, ok := r.(string); ok {
			num, err := strconv.Atoi(ret)
			if err != nil {
				return 0, err
			}
			return int64(num), nil
		}
	case int64:
		if ret, ok := r.(int64); ok {
			return ret, nil
		}
	case int:
		if ret, ok := r.(int); ok {
			return int64(ret), nil
		}
	default:
		return 0, fmt.Errorf("Failed to convert to int64: %v", v)
	}
	return 0, errors.New("Failed to convert to int64: " + exp)
}

func (q *JQ) QueryToInt64WithDefault(exp string, def int64, options ...Option) int64 {
	i, err := q.QueryToInt64(exp, options...)
	if err != nil {
		return def
	}
	return i
}

func (q *JQ) QueryToInt(exp string, options ...Option) (int, error) {
	num, err := q.QueryToInt64(exp, options...)
	return int(num), err
}

func (q *JQ) QueryToIntWithDefault(exp string, def int, options ...Option) int {
	num, err := q.QueryToInt(exp, options...)
	if err != nil {
		return def
	}
	return num
}

func (q *JQ) StyledString() string {
	return q.e.jq.StyledString()
}

func (q *JQ) String() string {
	return q.e.String()
}

func (q *JQ) Bytes() []byte {
	return q.e.Bytes()
}

var vldt = validator.New(validator.WithRequiredStructEnabled())

func (q *JQ) As(out interface{}) error {
	err := q.e.As(out)
	if err != nil {
		return fmt.Errorf("unable to decode key %q into object, error: %w", q.lastExp, err)
	}
	err = vldt.Struct(out)
	var invalidValidationError *validator.InvalidValidationError
	if errors.As(err, &invalidValidationError) {
		klog.Warningf("invalid validation error, not a pointer to struct: %v", invalidValidationError)
		return nil
	}
	return err
}

func (q *JQ) QueryToFloat64(exp string, options ...Option) (float64, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return 0, errors.New("Failed to parse: " + exp)
	}
	if ret, ok := r.(float64); ok {
		return ret, nil
	}
	return 0, errors.New("Failed to convert to float64: " + exp)
}

func (q *JQ) QueryToFloat64WithDefault(exp string, def float64, options ...Option) float64 {
	f, err := q.QueryToFloat64(exp, options...)
	if err != nil {
		return def
	}
	return f
}

func (q *JQ) ListKeys(options ...Option) ([]string, error) {
	opt := defaultQueryOptions()
	ExactlyQuery()(&opt)
	for _, fn := range options {
		fn(&opt)
	}
	var keys []string
	var err error
	if opt.cascade {
		keys, err = q.e.ListKeysCascade()
	} else {
		keys, err = q.e.ListKeys()
	}

	if err != nil {
		return nil, err
	}
	if !opt.caseCompatible {
		return keys, nil
	} else {
		for _, key := range keys {
			if strcase.ToSnake(key) == key {
				keys = append(keys, strcase.ToCamel(key))
			} else if strcase.ToCamel(key) == key {
				keys = append(keys, strcase.ToSnake(key))
			}
		}
		return keys, nil
	}
}

func (q *JQ) GetData() interface{} {
	return q.e.jq.data
}

func (q *JQ) Remove(exp string) error {
	return q.e.Remove(q.parseKey(exp))
}

// QueryToBool queries against the JSON with the expression passed in, and convert to bool
func (q *JQ) QueryToBool(exp string, options ...Option) (bool, error) {
	r, err := q.Query(exp, options...)
	if err != nil {
		return false, errors.New("Failed to parse: " + exp)
	}

	switch v := r.(type) {
	case string:
		if ret, ok := r.(string); ok {
			return strconv.ParseBool(ret)
		}
	case bool:
		if ret, ok := r.(bool); ok {
			return ret, nil
		}
	default:
		return false, fmt.Errorf("Failed to convert to bool: %v", v)
	}

	return false, errors.New("Failed to convert to bool: " + exp)
}

func (q *JQ) QueryToBoolWithDefault(exp string, def bool, options ...Option) bool {
	b, err := q.QueryToBool(exp, options...)
	if err != nil {
		return def
	}
	return b
}

// Has test whether the given expression is exists in JSON
func (q *JQ) Has(exp string, options ...Option) bool {
	_, err := q.Query(exp, options...)
	return err == nil
}

// IsBlank test whether the given expression not exists or has empty value
func (q *JQ) IsBlank(exp string, options ...Option) bool {
	v, err := q.Query(exp, options...)
	if err != nil {
		return true
	}
	if v == nil {
		return true
	}
	switch v.(type) {
	case string:
		return len(v.(string)) == 0
	case []interface{}:
		return len(v.([]interface{})) == 0
	case map[string]interface{}:
		return len(v.(map[string]interface{})) == 0
	}
	return false
}

func (q *JQ) ExactKey(exp string, options ...Option) string {
	ek := q.parseKey(exp)
	if !q.e.Has(&ek, options...) {
		return ""
	} else {
		return ek.exactKey
	}
}

// IsTrue test whether the given expression is True (also support string "true")
func (q *JQ) IsTrue(exp string, options ...Option) bool {
	ok, err := q.QueryToBool(exp, options...)
	if err != nil {
		okStr, err := q.QueryToString(exp, options...)
		if err == nil {
			okOut, err := strconv.ParseBool(okStr)
			if err == nil {
				ok = okOut
			}
		}
	}
	return ok
}

// OmitEmpty omits empty fields in cascade
func (q *JQ) OmitEmpty(exceptKeys ...string) {
	keys, err := q.ListKeys(Cascade())
	if err != nil {
		return
	}

	excepts := sets.NewString(exceptKeys...)
	for _, key := range keys {
		if excepts.Has(key) {
			continue
		}
		if q.IsBlank(key) {
			q.Remove(key)
		}
	}
}

func (q *JQ) Export() (string, error) {
	return q.e.Export()
}

// MergeWithoutOverwrite merges external configurations to current, and takes the current configuration item as priority.
// That is, if the currently specified configuration item does not exist, it will be overwritten, otherwise, just skip.
func (q *JQ) MergeWithoutOverwrite(newCfg *JQ) error {
	keySet, err := newCfg.ListKeys(ExactlyQuery())
	if err != nil {
		return err
	}
	for _, key := range keySet {
		val, err := newCfg.Query(key)
		if err != nil {
			return err
		}
		if val == nil {
			// just skip without remove
			continue
		}

		if !q.Has(key) {
			q.Set(key, val)
		} else {
			// just skip without overwrite
		}

	}
	return nil
}

// NewFileQuery - Create a new &JQ from a JSON file.
func NewFileQuery(jsonFile string) (*JQ, error) {
	raw, err := ioutil.ReadFile(jsonFile)
	if err != nil {
		return nil, err
	}
	var data = new(interface{})
	err = json.Unmarshal(raw, data)
	if err != nil {
		return nil, err
	}
	return NewEJQ(&jq{*data}), nil
}

// NewBytesQuery - Create a new JQ from raw JSON bytes.
func NewBytesQuery(jsonBytes []byte) (*JQ, error) {
	var data = new(interface{})

	err := json.Unmarshal(jsonBytes, data)
	if err != nil {
		return nil, err
	}
	return NewEJQ(&jq{*data}), nil
}

// NewStringQuery - Create a new JQ from a raw JSON string.
func NewStringQuery(jsonString string) (*JQ, error) {
	var data = new(interface{})

	err := json.Unmarshal([]byte(jsonString), data)
	if err != nil {
		return nil, err
	}
	return NewEJQ(&jq{*data}), nil
}

func MustNewStringQuery(jsonString string) *JQ {
	var data = new(interface{})
	err := json.Unmarshal([]byte(jsonString), data)
	if err != nil {
		panic(err)
	}
	return NewEJQ(&jq{*data})
}

func NewObjectQuery(obj any) (*JQ, error) {
	bytes, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return NewBytesQuery(bytes)
}

// NewQuery - Create a &JQ from an interface{} parsed by json.Unmarshal
func NewQuery(jsonObject interface{}) *JQ {
	return NewEJQ(&jq{data: jsonObject})
}

func NewQueryWithExp(jsonObject interface{}, exp string) *JQ {
	ret := NewEJQ(&jq{data: jsonObject})
	ret.lastExp = exp
	return ret
}

// StripDotedExceptFor transforms some given field to dot format
func StripDotedExceptFor(cfg *JQ, exceptKey string) {
	if cfg == nil || len(exceptKey) == 0 || !cfg.Has(exceptKey) {
		return
	}
	featMp, err := GetDottedStyleItem(cfg, exceptKey)
	if err == nil {
		cfg.Set(exceptKey, featMp)
	}
}

func ParseKey(input string) EquivalenceKey {
	parts := strings.Split(input, ",")
	opt := defaultQueryOptions()
	if len(parts) == 0 {
		panic("illegal query key")
	}

	if len(parts) == 1 {
		return EquivalenceKey{Preferred: parts[0], options: opt}
	} else {
		var keys []string
		var hint string
		for _, part := range parts[1:] {
			if strings.HasPrefix(part, "#") {
				hint = strings.TrimPrefix(part, "#")
				opt.addSuggestion = true
				continue
			}
			if strings.HasPrefix(part, "?") {
				switch {
				case strings.ContainsRune(part, 'k'):
					opt.removeRedundant = false
					fallthrough
				case strings.ContainsRune(part, 'h'):
					opt.hideOnExport = true
				}
				continue
			}

			keys = append(keys, part)
		}
		return EquivalenceKey{Preferred: parts[0], Keys: keys, hint: hint, options: opt}
	}
}

func CompareExp(jq1, jq2 *JQ, exp string) (bool, error) {
	has1 := jq1.Has(exp)
	has2 := jq2.Has(exp)
	if has1 != has2 {
		return false, nil
	}

	if !has1 && !has2 {
		return true, nil
	}

	val1, err1 := jq1.QueryToString(exp)
	if err1 != nil {
		return false, err1
	}

	val2, err2 := jq2.QueryToString(exp)
	if err2 != nil {
		return false, err2
	}

	return reflect.DeepEqual(val1, val2), nil
}

func CompareExps(jq1, jq2 *JQ, exps []string) (bool, error) {
	for _, exp := range exps {
		equal, err := CompareExp(jq1, jq2, exp)
		if err != nil {
			return false, err
		}

		if !equal {
			return false, nil
		}
	}

	return true, nil
}

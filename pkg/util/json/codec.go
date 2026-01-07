package json

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unsafe"

	"easgo/pkg/util/erraggr"
	"easgo/pkg/util/strcase"
	jsoniter "github.com/json-iterator/go"
	"github.com/modern-go/reflect2"
	"k8s.io/apimachinery/pkg/util/sets"
)

type funcEncoder struct {
	fun         jsoniter.EncoderFunc
	isEmptyFunc func(ptr unsafe.Pointer) bool
}

func (encoder *funcEncoder) Encode(ptr unsafe.Pointer, stream *jsoniter.Stream) {
	if encoder.fun != nil {
		encoder.fun(ptr, stream)
	}
}

func (encoder *funcEncoder) IsEmpty(ptr unsafe.Pointer) bool {
	if encoder.isEmptyFunc == nil {
		return false
	}
	return encoder.isEmptyFunc(ptr)
}

type jsonExtension struct {
	jsoniter.DummyExtension
}

func (extension *jsonExtension) UpdateStructDescriptor(structDescriptor *jsoniter.StructDescriptor) {
	for _, binding := range structDescriptor.Fields {
		binding.ToNames = []string{binding.Field.Name()}
	}
}

type ExtendedJSON struct {
	jsoniter.API
	staticExts []jsoniter.Extension
}

func (ext *ExtendedJSON) MarshallJSON(obj interface{}) ([]byte, error) {
	return ext.API.MarshalIndent(obj, "", "  ")
}

func NewExtendedJSON() *ExtendedJSON {
	api := jsoniter.Config{
		EscapeHTML:             true,
		SortMapKeys:            true,
		ValidateJsonRawMessage: true,
	}.Froze()
	var exts []jsoniter.Extension
	ext1 := &jsonExtension{}
	exts = append(exts, ext1)
	api.RegisterExtension(ext1)

	ext2 := &camelCompatibleNamingStrategyExtension{}
	exts = append(exts, ext2)
	api.RegisterExtension(ext2)
	return &ExtendedJSON{api, exts}
}

var DefaultExtendedJSON = NewExtendedJSON()

type camelCompatibleNamingStrategyExtension struct {
	jsoniter.DummyExtension
}

func (extension *camelCompatibleNamingStrategyExtension) UpdateStructDescriptor(structDescriptor *jsoniter.StructDescriptor) {
	for _, binding := range structDescriptor.Fields {
		if unicode.IsLower(rune(binding.Field.Name()[0])) || binding.Field.Name()[0] == '_' {
			continue
		}
		tag, hastag := binding.Field.Tag().Lookup("json")
		if hastag {
			tagParts := strings.Split(tag, ",")
			if tagParts[0] == "-" {
				continue // hidden field
			}

			binding.ToNames = sets.NewString(binding.ToNames...).Insert(tagParts[0], strcase.ToLowerCamel(tagParts[0]), strcase.ToSnake(tagParts[0])).UnsortedList()
			binding.FromNames = sets.NewString(binding.FromNames...).Insert(tagParts[0], strcase.ToLowerCamel(tagParts[0]), strcase.ToSnake(tagParts[0])).UnsortedList()
		}
	}
}

type funcDecoder struct {
	fn jsoniter.DecoderFunc
}

func (d *funcDecoder) Decode(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
	d.fn(ptr, iter)
}

// typeConvertingDecodeExtension makes automatic type-error correction during decoding for some golang basic types
type typeConvertingDecodeExtension struct {
	jsoniter.DummyExtension
	reportErr bool
	errorList []error
}

func newTypeConvertingDecoder(reportErr bool) *typeConvertingDecodeExtension {
	return &typeConvertingDecodeExtension{reportErr: reportErr, errorList: make([]error, 0)}
}

func jsonTagKeyOrFieldName(field reflect2.StructField) string {
	val, ok := field.Tag().Lookup("json")
	if !ok {
		return field.Name()
	}
	fields := strings.FieldsFunc(val, func(r rune) bool {
		return r == ',' || r == ' '
	})
	switch fields[0] {
	case "-", "inline", "omitempty", "":
		return field.Name()
	default:
		return fields[0]
	}
}

func signedIntDecoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*((*int)(ptr)) = int(iterVal.(int64))
		expectedType = true
	case reflect.Float64:
		*((*int)(ptr)) = int(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*int)(ptr)) = int(iterVal.(float32))
		expectedType = true
	case reflect.String:
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*int)(ptr)) = int(val)
		} else {
			*((*int)(ptr)) = 0
		}
	default:
		*((*int)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func signedInt8Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*((*int8)(ptr)) = int8(iterVal.(int64))
		expectedType = true
	case reflect.Float64:
		*((*int8)(ptr)) = int8(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*int8)(ptr)) = int8(iterVal.(float32))
		expectedType = true
	case reflect.String:
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*int8)(ptr)) = int8(val)
		} else {
			*((*int8)(ptr)) = 0
		}
	default:
		*((*int8)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func signedInt16Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*((*int16)(ptr)) = int16(iterVal.(int64))
		expectedType = true
	case reflect.Float64:
		*((*int16)(ptr)) = int16(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*int16)(ptr)) = int16(iterVal.(float32))
		expectedType = true
	case reflect.String:
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*int16)(ptr)) = int16(val)
		} else {
			*((*int16)(ptr)) = 0
		}
	default:
		*((*int16)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func signedInt32Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*((*int32)(ptr)) = int32(iterVal.(int64))
		expectedType = true
	case reflect.Float64:
		*((*int32)(ptr)) = int32(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*int32)(ptr)) = int32(iterVal.(float32))
		expectedType = true
	case reflect.String:
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*int32)(ptr)) = int32(val)
		} else {
			*((*int32)(ptr)) = 0
		}
	default:
		*((*int32)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func signedInt64Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*((*int64)(ptr)) = iterVal.(int64)
		expectedType = true
	case reflect.Float64:
		*((*int64)(ptr)) = int64(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*int64)(ptr)) = int64(iterVal.(float32))
		expectedType = true
	case reflect.String:
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*int64)(ptr)) = int64(val)
		} else {
			*((*int64)(ptr)) = 0
		}
	default:
		*((*int64)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func unsignedIntDecoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*((*uint)(ptr)) = uint(iterVal.(uint64))
		expectedType = true
	case reflect.Float64:
		*((*uint)(ptr)) = uint(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*uint)(ptr)) = uint(iterVal.(float32))
		expectedType = true
	case reflect.String:
		if strings.HasPrefix(iterVal.(string), "-") {
			*((*uint)(ptr)) = 0
			break
		}
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*uint)(ptr)) = uint(val)
		} else {
			*((*uint)(ptr)) = 0
		}
	default:
		*((*uint)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func unsignedInt8Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*((*uint8)(ptr)) = uint8(iterVal.(uint64))
		expectedType = true
	case reflect.Float64:
		*((*uint8)(ptr)) = uint8(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*uint8)(ptr)) = uint8(iterVal.(float32))
		expectedType = true
	case reflect.String:
		if strings.HasPrefix(iterVal.(string), "-") {
			*((*uint8)(ptr)) = 0
			break
		}
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*uint8)(ptr)) = uint8(val)
		} else {
			*((*uint8)(ptr)) = 0
		}
	default:
		*((*uint)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func unsignedInt16Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*((*uint16)(ptr)) = uint16(iterVal.(uint64))
		expectedType = true
	case reflect.Float64:
		*((*uint16)(ptr)) = uint16(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*uint16)(ptr)) = uint16(iterVal.(float32))
		expectedType = true
	case reflect.String:
		if strings.HasPrefix(iterVal.(string), "-") {
			*((*uint16)(ptr)) = 0
			break
		}
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*uint16)(ptr)) = uint16(val)
		} else {
			*((*uint16)(ptr)) = 0
		}
	default:
		*((*uint16)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func unsignedInt32Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*((*uint32)(ptr)) = uint32(iterVal.(uint64))
		expectedType = true
	case reflect.Float64:
		*((*uint32)(ptr)) = uint32(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*uint32)(ptr)) = uint32(iterVal.(float32))
		expectedType = true
	case reflect.String:
		if strings.HasPrefix(iterVal.(string), "-") {
			*((*int32)(ptr)) = 0
			break
		}
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*uint32)(ptr)) = uint32(val)
		} else {
			*((*uint32)(ptr)) = 0
		}
	default:
		*((*int32)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func unsignedInt64Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*((*uint64)(ptr)) = uint64(iterVal.(uint64))
		expectedType = true
	case reflect.Float64:
		*((*uint64)(ptr)) = uint64(iterVal.(float64))
		expectedType = true
	case reflect.Float32:
		*((*uint64)(ptr)) = uint64(iterVal.(float32))
		expectedType = true
	case reflect.String:
		if strings.HasPrefix(iterVal.(string), "-") {
			*((*uint64)(ptr)) = 0
			break
		}
		val, err := strconv.Atoi(iterVal.(string))
		if err == nil {
			*((*uint64)(ptr)) = uint64(val)
		} else {
			*((*uint64)(ptr)) = 0
		}
	default:
		*((*uint64)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func float32Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Float64, reflect.Float32:
		*((*float32)(ptr)) = float32(iterVal.(float64))
		expectedType = true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		expectedType = true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*((*float32)(ptr)) = float32(iterVal.(uint64))
		expectedType = true
	case reflect.String:
		val, err := strconv.ParseFloat(iterVal.(string), int(unsafe.Sizeof(float32(0))*8))
		if err == nil {
			*((*float32)(ptr)) = float32(val)
		} else {
			*((*float32)(ptr)) = 0
		}
	default:
		*((*float32)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func float64Decoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.Float64, reflect.Float32:
		*((*float64)(ptr)) = float64(iterVal.(float64))
		expectedType = true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*((*float64)(ptr)) = float64(iterVal.(int64))
		expectedType = true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*((*float64)(ptr)) = float64(iterVal.(uint64))
		expectedType = true
	case reflect.String:
		val, err := strconv.ParseFloat(iterVal.(string), int(unsafe.Sizeof(float64(0))*8))
		if err == nil {
			*((*float64)(ptr)) = float64(val)
		} else {
			*((*float64)(ptr)) = 0
		}
	default:
		*((*float64)(ptr)) = 0
	}

	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func stringDecoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterType := reflect.TypeOf(iterVal)
	if iterType == nil {
		ptr = nil
		return
	}
	iterKind := reflect.TypeOf(iterVal).Kind()
	switch iterKind {
	case reflect.String:
		*((*string)(ptr)) = iterVal.(string)
		expectedType = true
	default:
		*((*string)(ptr)) = fmt.Sprintf("%v", iterVal)
	}
	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func boolDecoder(ptr unsafe.Pointer, iter *jsoniter.Iterator, bind *jsoniter.Binding, ext *typeConvertingDecodeExtension) {
	expectedType := false
	iterVal := iter.Read()
	iterKind := reflect.TypeOf(iterVal).Kind()
	if iterKind == reflect.Bool {
		*((*bool)(ptr)) = iterVal.(bool)
		expectedType = true
	} else if iterKind == reflect.String {
		val, err := strconv.ParseBool(iterVal.(string))
		if err == nil {
			*((*bool)(ptr)) = val
		}
	} else {
		*((*bool)(ptr)) = false
	}
	if !expectedType {
		err := fmt.Errorf("unsupported value %T(%v) for field: %v, expected type: %v", iterVal, iterVal, jsonTagKeyOrFieldName(bind.Field), bind.Field.Type().Kind())
		if ext.reportErr {
			ext.errorList = append(ext.errorList, err)
		}
	}
}

func (d *typeConvertingDecodeExtension) UpdateStructDescriptor(structDescriptor *jsoniter.StructDescriptor) {
	if structDescriptor == nil {
		return
	}
	for _, bindF := range structDescriptor.Fields {
		bind := bindF
		switch bind.Field.Type().Kind() {
		case reflect.String:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				stringDecoder(ptr, iter, bind, d)
			}}

		case reflect.Int64:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				signedInt64Decoder(ptr, iter, bind, d)
			}}

		case reflect.Int32:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				signedInt32Decoder(ptr, iter, bind, d)
			}}

		case reflect.Int16:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				signedInt16Decoder(ptr, iter, bind, d)
			}}

		case reflect.Int8:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				signedInt8Decoder(ptr, iter, bind, d)
			}}

		case reflect.Int:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				signedIntDecoder(ptr, iter, bind, d)
			}}

		case reflect.Uint64:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				unsignedInt64Decoder(ptr, iter, bind, d)
			}}

		case reflect.Uint32:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				unsignedInt32Decoder(ptr, iter, bind, d)
			}}

		case reflect.Uint16:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				unsignedInt16Decoder(ptr, iter, bind, d)
			}}

		case reflect.Uint8:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				unsignedInt8Decoder(ptr, iter, bind, d)
			}}

		case reflect.Uint:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				unsignedIntDecoder(ptr, iter, bind, d)
			}}

		case reflect.Float32:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				float32Decoder(ptr, iter, bind, d)
			}}

		case reflect.Float64:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				float64Decoder(ptr, iter, bind, d)
			}}

		case reflect.Bool:
			bind.Decoder = &funcDecoder{func(ptr unsafe.Pointer, iter *jsoniter.Iterator) {
				boolDecoder(ptr, iter, bind, d)
			}}
		}
	}
}

func (d *typeConvertingDecodeExtension) error() []error {
	var errorList []error
	errorList = append(errorList, d.errorList...)
	return errorList
}

func (d *typeConvertingDecodeExtension) clear() {
	if len(d.errorList) > 0 {
		d.errorList = d.errorList[:0]
	}
}

type AutoTypeConverting struct {
	jsoniter.API
	*typeConvertingDecodeExtension
}

func (js *AutoTypeConverting) UnmarshalWithExtension(data []byte, v interface{}) []error {
	defer js.clear()
	err := js.API.Unmarshal(data, v)
	if err != nil {
		return js.error()
	}
	return js.error()
}

func (js *AutoTypeConverting) Unmarshal(data []byte, v interface{}) error {
	return erraggr.NewErrAggregator(js.UnmarshalWithExtension(data, v))
}

func (js *AutoTypeConverting) UnmarshalFromString(data string, v interface{}) error {
	return erraggr.NewErrAggregator(js.UnmarshalWithExtension([]byte(data), v))
}

func (ext *ExtendedJSON) WithAutoTypeConverting(reportErr bool) *ExtendedJSON {
	autoExt := newTypeConvertingDecoder(reportErr)
	config := jsoniter.Config{
		EscapeHTML:             true,
		SortMapKeys:            true,
		ValidateJsonRawMessage: true,
	}.Froze()

	for _, ex := range ext.staticExts {
		config.RegisterExtension(ex)
	}
	config.RegisterExtension(autoExt)
	auto := &AutoTypeConverting{API: config, typeConvertingDecodeExtension: autoExt}
	return &ExtendedJSON{API: auto, staticExts: ext.staticExts}
}

type noOmitDecorateCodec struct {
	originEncoder jsoniter.ValEncoder
}

func (c *noOmitDecorateCodec) Encode(ptr unsafe.Pointer, stream *jsoniter.Stream) {
	c.originEncoder.Encode(ptr, stream)
}

func (c *noOmitDecorateCodec) IsEmpty(ptr unsafe.Pointer) bool {
	// always return false to ignore omitempty option
	return false
}

type noOmitExtension struct {
	jsoniter.DummyExtension
}

func (extension *noOmitExtension) DecorateEncoder(typ reflect2.Type, encoder jsoniter.ValEncoder) jsoniter.ValEncoder {
	return &noOmitDecorateCodec{originEncoder: encoder}
}

func newNoOmitJSON() jsoniter.API {
	api := jsoniter.Config{
		EscapeHTML:             true,
		SortMapKeys:            true,
		ValidateJsonRawMessage: true,
	}.Froze()
	api.RegisterExtension(&noOmitExtension{})
	return api
}

var NoOmitJSON = newNoOmitJSON()

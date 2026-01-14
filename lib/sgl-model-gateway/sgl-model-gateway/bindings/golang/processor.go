package sglang

/*
#cgo LDFLAGS: -lsgl_model_gateway_go -lm -ldl 
#include <stdlib.h>
#include <stdint.h>

// Error codes
typedef enum {
    SGL_ERROR_SUCCESS = 0,
    SGL_ERROR_INVALID_ARGUMENT = 1,
    SGL_ERROR_TOKENIZATION_ERROR = 2,
    SGL_ERROR_PARSING_ERROR = 3,
    SGL_ERROR_MEMORY_ERROR = 4,
    SGL_ERROR_UNKNOWN = 99
} SglErrorCode;

// Tokenizer handles
typedef void* TokenizerHandle;

// Tokenizer functions
TokenizerHandle* sgl_tokenizer_create_from_file(const char* path, char** error_out);
TokenizerHandle* sgl_tokenizer_create_from_file_with_chat_template(const char* path, const char* chat_template_path, char** error_out);
SglErrorCode sgl_tokenizer_encode(TokenizerHandle* handle, const char* text, int add_special_tokens, uint32_t** token_ids_out, size_t* token_count_out, char** error_out);
SglErrorCode sgl_tokenizer_apply_chat_template(TokenizerHandle* handle, const char* messages_json, char** result_out, char** error_out);
SglErrorCode sgl_tokenizer_apply_chat_template_with_tools(TokenizerHandle* handle, const char* messages_json, const char* tools_json, char** result_out, char** error_out);
SglErrorCode sgl_tokenizer_apply_chat_template_with_tools_and_params(TokenizerHandle* handle, const char* messages_json, const char* tools_json, const char* params_json, char** result_out, char** error_out);
SglErrorCode sgl_tokenizer_decode(TokenizerHandle* handle, const uint32_t* token_ids, size_t token_count, int skip_special_tokens, char** result_out, char** error_out);
void sgl_tokenizer_free(TokenizerHandle* handle);
void sgl_free_string(char* s);
void sgl_free_token_ids(uint32_t* ptr, size_t count);
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/sglang/sglang-go-grpc-sdk/internal/ffi"
)

type Tokenizer struct {
	handle *C.TokenizerHandle
}

func CreateTokenizerFromFile(path string) (*Tokenizer, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var errorPtr *C.char
	handle := C.sgl_tokenizer_create_from_file(cPath, &errorPtr)
	if handle == nil {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)
		}
		if errorMsg == "" {
			errorMsg = "failed to create tokenizer from file"
		}
		return nil, fmt.Errorf("%s", errorMsg)
	}
	return &Tokenizer{handle: handle}, nil
}

func CreateTokenizerFromFileWithChatTemplate(path, chatTemplatePath string) (*Tokenizer, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cChatTemplatePath := C.CString(chatTemplatePath)
	defer C.free(unsafe.Pointer(cChatTemplatePath))

	var errorPtr *C.char
	handle := C.sgl_tokenizer_create_from_file_with_chat_template(cPath, cChatTemplatePath, &errorPtr)
	if handle == nil {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)
		}
		if errorMsg == "" {
			errorMsg = "failed to create tokenizer from file"
		}
		return nil, fmt.Errorf("%s", errorMsg)
	}
	return &Tokenizer{handle: handle}, nil
}

func (t *Tokenizer) Free() {
	if t.handle != nil {
		C.sgl_tokenizer_free(t.handle)
		t.handle = nil
	}
}

func (t *Tokenizer) Encode(text string, addSpecialToken bool) ([]uint32, error) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	var cAddSpecialTokens C.int
	if addSpecialToken {
		cAddSpecialTokens = 1
	} else {
		cAddSpecialTokens = 0
	}

	var tokenIdsPtr *C.uint32_t
	var tokenCount C.size_t
	var errorPtr *C.char
	errCode := C.sgl_tokenizer_encode(t.handle, cText, cAddSpecialTokens, &tokenIdsPtr, &tokenCount, &errorPtr)
	if errCode != C.SGL_ERROR_SUCCESS {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)
		}
		if errorMsg == "" {
			errorMsg = "tokenization error"
		}
		return nil, fmt.Errorf("%s", errorMsg)
	}

	// defer C.free(unsafe.Pointer(tokenIdsPtr))
	defer C.sgl_free_token_ids(tokenIdsPtr, tokenCount)

	tokenCountGo := int(tokenCount)
	tokenIds := make([]uint32, tokenCountGo)
	// slice := (*[1 << 30]C.uint32_t)(unsafe.Pointer(tokenIdsPtr))[:tokenCountGo:tokenCountGo]
	slice := unsafe.Slice(tokenIdsPtr, tokenCountGo) //  unsafe.Slice requires Go 1.17+
	for i := 0; i < tokenCountGo; i++ {
		tokenIds[i] = uint32(slice[i])
	}

	return tokenIds, nil

}

func (t *Tokenizer) ApplyChatTemplate(messagesJson string) (string, error) {
	cMessagesJson := C.CString(messagesJson)
	defer C.free(unsafe.Pointer(cMessagesJson))

	var resultPtr *C.char
	var errorPtr *C.char
	errCode := C.sgl_tokenizer_apply_chat_template(t.handle, cMessagesJson, &resultPtr, &errorPtr)
	if errCode != C.SGL_ERROR_SUCCESS {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)
		}
		if errorMsg == "" {
			errorMsg = "error applying chat template"
		}
		return "", fmt.Errorf("%s", errorMsg)
	}
	defer C.sgl_free_string(resultPtr)

	result := C.GoString(resultPtr)
	return result, nil
}

func (t *Tokenizer) ApplyChatTemplateWithTools(messagesJson, toolsJson string) (string, error) {

	cMessagesJson := C.CString(messagesJson)
	defer C.free(unsafe.Pointer(cMessagesJson))
	cToolsJson := C.CString(toolsJson)
	defer C.free(unsafe.Pointer(cToolsJson))

	var resultPtr *C.char
	var errorPtr *C.char
	errCode := C.sgl_tokenizer_apply_chat_template_with_tools(t.handle, cMessagesJson, cToolsJson, &resultPtr, &errorPtr)
	if errCode != C.SGL_ERROR_SUCCESS {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)

			return "", fmt.Errorf("%s", errorMsg)
		}
	}
	defer C.sgl_free_string(resultPtr)
	result := C.GoString(resultPtr)
	return result, nil
}

// func (t *Tokenizer) ApplyChatTemplateWithToolsAndParams(messagesJson, toolsJson, paramsJson string) (string, error) {
// 	cMessagesJson := C.CString(messagesJson)
// 	defer C.free(unsafe.Pointer(cMessagesJson))
// 	cToolsJson := C.CString(toolsJson)
// 	defer C.free(unsafe.Pointer(cToolsJson))
// 	cParamsJson := C.CString(paramsJson)
// 	defer C.free(unsafe.Pointer(cParamsJson))

// 	var resultPtr *C.char
// 	var errorPtr *C.char
// 	errCode := C.sgl_tokenizer_apply_chat_template_with_tools_and_params(t.handle, cMessagesJson, cToolsJson, cParamsJson, &resultPtr, &errorPtr)
// 	if errCode != C.SGL_ERROR_SUCCESS {
// 		errorMsg := ""
// 		if errorPtr != nil {
// 			errorMsg = C.GoString(errorPtr)
// 			C.sgl_free_string(errorPtr)
// 		}
// 		if errorMsg == "" {
// 			errorMsg = "error applying chat template"
// 		}
// 		return "", fmt.Errorf("%s", errorMsg)
// 	}
// 	defer C.sgl_free_string(resultPtr)

// 	result := C.GoString(resultPtr)
// 	return result, nil
// }

func (t *Tokenizer) Decode(tokenIds []uint32, skipSpecialTokens bool) (string, error) {
	cTokenCount := C.size_t(len(tokenIds))
	var cSkipSpecialTokens C.int
	if skipSpecialTokens {
		cSkipSpecialTokens = 1
	} else {
		cSkipSpecialTokens = 0
	}

	cTokenIds := (*C.uint32_t)(unsafe.Pointer(&tokenIds[0]))
	var resultPtr *C.char
	var errorPtr *C.char
	errCode := C.sgl_tokenizer_decode(t.handle, cTokenIds, cTokenCount, cSkipSpecialTokens, &resultPtr, &errorPtr)
	if errCode != C.SGL_ERROR_SUCCESS {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)
		}
		if errorMsg == "" {
			errorMsg = "decoding error"
		}
		return "", fmt.Errorf("%s", errorMsg)
	}
	defer C.sgl_free_string(resultPtr)

	result := C.GoString(resultPtr)
	return result, nil
}

type PreprocessedRequest = ffi.PreprocessedRequest

func (t *Tokenizer) toFFITokenizerHandle() *ffi.TokenizerHandle {
	return ffi.CopyTokenizerHandle(unsafe.Pointer(t.handle))
}
func (t *Tokenizer) PreProcessChatRequest(requestJson string) (*PreprocessedRequest, error) {
	return ffi.PreprocessChatRequestWithTokenizer(requestJson, t.toFFITokenizerHandle())
}

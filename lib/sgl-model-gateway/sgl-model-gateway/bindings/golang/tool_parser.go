package sglang

/*
#cgo LDFLAGS: -lsgl_model_gateway_go -ldl
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

// Tool Parser handles
typedef void* ToolParserHandle;

// Tool Parser functions
ToolParserHandle* sgl_tool_parser_create(const char* tools_json, char** error_out);
SglErrorCode sgl_tool_parser_parse_complete(ToolParserHandle* handle, const char* text, char** result_json_out, char** error_out);
SglErrorCode sgl_tool_parser_parse_incremental(ToolParserHandle* handle, const char* chunk, const char* tool_json, char** result_json_out, char** error_out);
void sgl_tool_parser_free(ToolParserHandle* handle);
void sgl_tool_parser_reset(ToolParserHandle* handle);
void sgl_free_string(char* s);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type ToolParser struct {
	handle *C.ToolParserHandle
}

func NewToolParser(parserType string) (*ToolParser, error) {
	cParserType := C.CString(parserType)
	defer C.free(unsafe.Pointer(cParserType))

	var errorPtr *C.char
	handle := C.sgl_tool_parser_create(cParserType, &errorPtr)
	if handle == nil {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)
		}
		if errorMsg == "" {
			errorMsg = "failed to create tool parser"
		}
		return nil, fmt.Errorf("%s", errorMsg)
	}
	return &ToolParser{handle: handle}, nil
}

func (p *ToolParser) ParseComplete(text string) (string, error) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	var cResultJsonOut *C.char
	var errorPtr *C.char
	errorCode := C.sgl_tool_parser_parse_complete(p.handle, cText, &cResultJsonOut, &errorPtr)
	if errorCode != C.SGL_ERROR_SUCCESS {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)
		}
		if errorMsg == "" {
			errorMsg = "failed to parse tool response"
		}
		return "", fmt.Errorf("%s", errorMsg)
	}
	resultJson := C.GoString(cResultJsonOut)
	C.sgl_free_string(cResultJsonOut)
	return resultJson, nil
}

func (p *ToolParser) ParseStreamIncremental(chunk, toolJson string) (string, error) {
	cChunk := C.CString(chunk)
	defer C.free(unsafe.Pointer(cChunk))
	toolJsonC := C.CString(toolJson)
	defer C.free(unsafe.Pointer(toolJsonC))

	var cResultJsonOut *C.char
	var errorPtr *C.char
	errorCode := C.sgl_tool_parser_parse_incremental(p.handle, cChunk, toolJsonC, &cResultJsonOut, &errorPtr)
	if errorCode != C.SGL_ERROR_SUCCESS {
		errorMsg := ""
		if errorPtr != nil {
			errorMsg = C.GoString(errorPtr)
			C.sgl_free_string(errorPtr)
		}
		if errorMsg == "" {
			errorMsg = "failed to parse tool stream chunk"
		}
		return "", fmt.Errorf("%s", errorMsg)
	}
	resultJson := C.GoString(cResultJsonOut)
	C.sgl_free_string(cResultJsonOut)
	return resultJson, nil
}

func (p *ToolParser) Reset() {
	if p.handle != nil {
		C.sgl_tool_parser_reset(p.handle)
		p.handle = nil
	}
}

func (p *ToolParser) Free() {
	if p.handle != nil {
		C.sgl_tool_parser_free(p.handle)
		p.handle = nil
	}
}

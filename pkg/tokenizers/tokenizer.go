package tokenizers

/*
#cgo LDFLAGS: -ltokenizers_rs -ldl -lm -lstdc++
#include <stdlib.h>
#include "tokenizers.h"

// Link-time version check: this will fail to link if the library version doesn't match
// Using a global variable that references the function ensures the linker must resolve it
extern void tokenizers_version_1_23_0(void);
void (*tokenizers_version_check)(void) = &tokenizers_version_1_23_0;
*/
import "C"

// NOTE: There should be NO space between the comments and the `import "C"` line.
import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"
)

const baseURL = "https://huggingface.co"

// List of necessary tokenizer files and their mandatory status.
// True means mandatory, false means optional.
var tokenizerFiles = map[string]bool{
	"tokenizer.json":          true,
	"vocab.txt":               false,
	"merges.txt":              false,
	"special_tokens_map.json": false,
	"added_tokens.json":       false,
}

type Tokenizer struct {
	tokenizer unsafe.Pointer
}

var _ io.Closer = (*Tokenizer)(nil)

func FromFile(file_path string) (*Tokenizer, error) {
	return FromFileWithChatTemplate(file_path, "")
}

func FromFileWithChatTemplate(model_dir_path string, chat_template_path string) (*Tokenizer, error) {
	cModelDirPath := C.CString(model_dir_path)
	defer C.free(unsafe.Pointer(cModelDirPath))

	cChatTemplatePath := C.CString(chat_template_path)
	defer C.free(unsafe.Pointer(cChatTemplatePath))

	var errPtr *C.char
	tokenizer := C.tokenizers_from_file_with_chat_template(cModelDirPath, cChatTemplatePath, &errPtr)
	if tokenizer == nil {
		if errPtr != nil {
			errStr := C.GoString(errPtr)
			C.tokenizers_free_string(errPtr)
			return nil, fmt.Errorf("%s", errStr)
		}
		return nil, fmt.Errorf("failed to create tokenizer from file")
	}

	return &Tokenizer{tokenizer: tokenizer}, nil
}

type tokenizerConfig struct {
	cacheDir  *string
	authToken *string
}

type TokenizerConfigOption func(cfg *tokenizerConfig)

func WithCacheDir(path string) TokenizerConfigOption {
	return func(cfg *tokenizerConfig) {
		cfg.cacheDir = &path
	}
}

func WithAuthToken(token string) TokenizerConfigOption {
	return func(cfg *tokenizerConfig) {
		cfg.authToken = &token
	}
}

// FromPretrained downloads necessary files and initializes the tokenizer.
// Parameters:
//   - modelID: The Hugging Face model identifier (e.g., "bert-base-uncased").
//   - destination: Optional. If provided and not nil, files will be downloaded to this folder.
//     If nil, a temporary directory will be used.
//   - authToken: Optional. If provided and not nil, it will be used to authenticate requests.
func FromPretrained(modelID string, opts ...TokenizerConfigOption) (*Tokenizer, error) {
	cfg := &tokenizerConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if strings.TrimSpace(modelID) == "" {
		return nil, fmt.Errorf("modelID cannot be empty")
	}

	// Construct the model URL
	modelURL := ""
	hfEndpoint, exist := os.LookupEnv("HF_ENDPOINT")
	if exist {
		modelURL = fmt.Sprintf("%s/%s/resolve/main", hfEndpoint, modelID)
	} else {
		modelURL = fmt.Sprintf("%s/%s/resolve/main", baseURL, modelID)
	}

	// Determine the download directory
	var downloadDir string
	if cfg.cacheDir != nil {
		downloadDir = fmt.Sprintf("%s/%s", *cfg.cacheDir, modelID)
		// Create the destination directory if it doesn't exist
		err := os.MkdirAll(downloadDir, os.ModePerm)
		if err != nil {
			return nil, fmt.Errorf("failed to create destination directory %s: %w", downloadDir, err)
		}
	} else {
		// Create a temporary directory
		tmpDir, err := os.MkdirTemp("", "huggingface-tokenizer-*")
		if err != nil {
			return nil, fmt.Errorf("error creating temporary directory: %w", err)
		}
		downloadDir = tmpDir
	}

	var wg sync.WaitGroup
	errCh := make(chan error)

	// Download each tokenizer file concurrently
	for filename, isMandatory := range tokenizerFiles {
		wg.Add(1)
		go func(fn string, mandatory bool) {
			defer wg.Done()
			fileURL := fmt.Sprintf("%s/%s", modelURL, fn)
			destPath := filepath.Join(downloadDir, fn)
			err := downloadFile(fileURL, destPath, cfg.authToken)
			if err != nil && mandatory {
				// If the file is mandatory, report an error
				errCh <- fmt.Errorf("failed to download mandatory file %s: %w", fn, err)
			}
		}(filename, isMandatory)
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		if err := os.RemoveAll(downloadDir); err != nil {
			fmt.Printf("Warning: failed to clean up directory %s: %v\n", downloadDir, err)
		}
		return nil, errs[0]
	}

	return FromFileWithChatTemplate(filepath.Join(downloadDir, "tokenizer.json"), "")
}

// downloadFile downloads a file from the given URL and saves it to the specified destination.
// If authToken is provided (non-nil), it will be used for authorization.
// Returns an error if the download fails.
func downloadFile(url, destination string, authToken *string) error {
	// Check if the file already exists
	if _, err := os.Stat(destination); err == nil {
		return nil
	}

	// Create a new HTTP request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for %s: %w", url, err)
	}

	// If authToken is provided, set the Authorization header
	if authToken != nil {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *authToken))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download from %s: %w", url, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download from %s: status code %d", url, resp.StatusCode)
	}

	// Create the destination file
	out, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", destination, err)
	}
	defer out.Close()

	// Write the response body to the file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write to file %s: %w", destination, err)
	}

	fmt.Printf("Successfully downloaded %s\n", destination)
	return nil
}

func (t *Tokenizer) Close() error {
	C.tokenizers_free_tokenizer(t.tokenizer)
	t.tokenizer = nil
	return nil
}

type Encoding struct {
	IDs []uint32
}

func uintVecToSlice(arrPtr *C.uint, len int) []uint32 {
	arr := unsafe.Slice(arrPtr, len)
	slice := make([]uint32, len)
	for i, v := range arr {
		slice[i] = uint32(v)
	}
	return slice
}

func (t *Tokenizer) Encode(str string, addSpecialTokens bool) []uint32 {
	cStr := C.CString(str)
	defer C.free(unsafe.Pointer(cStr))

	res := C.tokenizers_encode(t.tokenizer, cStr, C.bool(addSpecialTokens))
	len := int(res.len)
	if len == 0 {
		return nil
	}
	defer C.tokenizers_free_buffer(res)

	ids := uintVecToSlice(res.ids, len)

	return ids
}

func (t *Tokenizer) Decode(tokenIDs []uint32, skipSpecialTokens bool) string {
	if len(tokenIDs) == 0 {
		return ""
	}
	len := C.uint(len(tokenIDs))
	res := C.tokenizers_decode(t.tokenizer, (*C.uint)(unsafe.Pointer(&tokenIDs[0])), len, C.bool(skipSpecialTokens))
	defer C.tokenizers_free_string(res)
	return C.GoString(res)
}

func (t *Tokenizer) VocabSize() uint32 {
	return uint32(C.tokenizers_vocab_size(t.tokenizer))
}

func (t *Tokenizer) MaxModelLen() uint64 {
	return uint64(C.tokenizers_model_max_len(t.tokenizer))
}

func (t *Tokenizer) ApplyChatTemplate(messages string, tools, params string) (string, error) {
	cMessages := C.CString(messages)
	defer C.free(unsafe.Pointer(cMessages))
	cTools := C.CString(tools)
	defer C.free(unsafe.Pointer(cTools))
	cParams := C.CString(params)
	defer C.free(unsafe.Pointer(cParams))

	var cError *C.char
	result := C.tokenizers_apply_chat_template(t.tokenizer, cMessages, cTools, cParams, &cError)
	defer C.tokenizers_free_string(result)
	defer C.tokenizers_free_string(cError)
	if cError != nil {
		err := C.GoString(cError)
		return "", fmt.Errorf("failed to apply chat template: %s", err)
	}
	// Convert result to Go string
	return C.GoString(result), nil

}

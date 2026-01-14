package tokenizer

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sglang/sglang-go-grpc-sdk"

	"k8s.io/klog/v2"
)

var (
	builtInTokenizerDir = "/tokenizers"
	builtInTokenizer    = map[string]*sglang.Tokenizer{}
)

var (
	tkOnce     sync.Once
	gTokenizer *sglang.Tokenizer
)

func newTokenizer(name string, path string, chatTemplatePath string) (*sglang.Tokenizer, error) {
	if name == "" && path == "" {
		return nil, nil
	}
	if path != "" {
		klog.Infof("loading tokenizer: %v", path)
		if chatTemplatePath != "" {
			klog.Infof("loading chat template: %v", chatTemplatePath)
		}
		return sglang.CreateTokenizerFromFileWithChatTemplate(path, chatTemplatePath)
	}
	klog.Infof("loading builtin tokenizer: %v", name)
	if t, ok := builtInTokenizer[name]; ok {
		return t, nil
	} else {
		return nil, fmt.Errorf("not support tokenizer %s", name)
	}
}

func InitTokenizer(name, path, chatTemplatePath string) {
	if name == "" && path == "" {
		return
	}
	tkOnce.Do(func() {
		tk, err := newTokenizer(name, path, chatTemplatePath)
		if err != nil {
			klog.Errorf("Could not load the tokenizer: %v", err)
		}
		gTokenizer = tk
	})
}

func GetTokenizer() (*sglang.Tokenizer, error) {
	if gTokenizer == nil {
		return nil, fmt.Errorf("tokenizer not been loaded")
	}
	return gTokenizer, nil
}

func init() {
	// scan the builtin tokenizer dir, take the dir name as the tokenizer name, and construct the file tokenizer
	if _, err := os.Stat(builtInTokenizerDir); err != nil {
		return
	}
	if files, err := os.ReadDir(builtInTokenizerDir); err == nil {
		for _, file := range files {
			if file.IsDir() {
				path := filepath.Join(builtInTokenizerDir, file.Name())
				tokenizer, err := sglang.CreateTokenizerFromFileWithChatTemplate(path, "")
				if err != nil {
					klog.Warningf("failed to load builtin tokenizer %s, error: %v", file.Name(), err)
					continue
				}
				builtInTokenizer[file.Name()] = tokenizer
			}
		}
	}
}

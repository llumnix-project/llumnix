package tokenizer

import (
	"fmt"
	"os"

	tokenizers "easgo/pkg/tokenizers"
)

type fileTokenizer struct {
	tk *tokenizers.Tokenizer
}

func NewFileTokenizer(path, chatTemplatePath string) (Tokenizer, error) {
	_, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("fail to stat tokenizer path %s", path)
	}

	var tk *tokenizers.Tokenizer
	// load model from file
	if chatTemplatePath == "" {
		tk, err = tokenizers.FromFile(path)
	} else {
		tk, err = tokenizers.FromFileWithChatTemplate(path, chatTemplatePath)
	}
	if err != nil {
		return nil, fmt.Errorf("fail to load tokenizer file %s:%v", path, err)
	}
	return &fileTokenizer{
		tk: tk,
	}, nil
}

func (f *fileTokenizer) Encode(text string, addSpecialTokens bool) ([]uint32, error) {
	ids := f.tk.Encode(text, addSpecialTokens)
	return ids, nil
}

func (f *fileTokenizer) Decode(ids []uint32, skipSpecialTokens bool) string {
	str := f.tk.Decode(ids, skipSpecialTokens)
	return str
}

func (f *fileTokenizer) ApplyChatTemplate(messages, tools, params string) (string, error) {
	return f.tk.ApplyChatTemplate(messages, tools, params)
}

func (f *fileTokenizer) MaxModelLen() uint64 {
	return f.tk.MaxModelLen()
}

func (f *fileTokenizer) VocabSize() uint32 {
	return f.tk.VocabSize()
}

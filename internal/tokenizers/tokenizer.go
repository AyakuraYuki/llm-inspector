package tokenizers

import (
	"fmt"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
)

// New creates a new tokenizer with model specified tokenizer config JSON file.
func New(configPath string) (*tokenizer.Tokenizer, error) {
	tk, err := pretrained.FromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("tokenizer loading failed: %w", err)
	}
	return tk, err
}

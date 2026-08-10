package dataset

import (
	"embed"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
)

const (
	AIME25  = "aime25"
	AIME26  = "aime26"
	MMLUPro = "MMLU_Pro"
)

//go:embed hf
var hfDatasetFS embed.FS

type Config struct {
	AIME25  bool          `yaml:"aime25"`
	AIME26  bool          `yaml:"aime26"`
	MMLUPro MMLUProConfig `yaml:"mmlu_pro"`
}

func (cfg *Config) LoadProblems() ([]types.Question, error) {
	var questions []types.Question

	aime25Problems, err := cfg.aime25()
	if err != nil {
		return nil, err
	}
	questions = append(questions, aime25Problems...)

	aime26Problems, err := cfg.aime26()
	if err != nil {
		return nil, err
	}
	questions = append(questions, aime26Problems...)

	mmluProValidations, err := cfg.MMLUPro.validations()
	if err != nil {
		return nil, err
	}
	questions = append(questions, mmluProValidations...)

	mmluProQuestions, err := cfg.MMLUPro.pickup()
	if err != nil {
		return nil, err
	}
	questions = append(questions, mmluProQuestions...)

	return questions, nil
}

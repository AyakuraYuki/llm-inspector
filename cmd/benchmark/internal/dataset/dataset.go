package dataset

import (
	"embed"
	"fmt"
	"sort"

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

	AIME25Problems []int `yaml:"aime25_problems"`
	AIME26Problems []int `yaml:"aime26_problems"`
}

// pickProblems 把 1-based 题号列表解析成 0-based 下标，返回的下标按升序排列，
// 保证选中题目始终按题库原始顺序进入测试。题号留空表示全选，重复题号会去重，
// 越界题号直接报错而不是静默跳过，避免选错题还以为是跑完了预期范围。
func pickProblems(total int, picked []int, dataset string) ([]int, error) {
	if len(picked) == 0 {
		indices := make([]int, total)
		for i := range indices {
			indices[i] = i
		}
		return indices, nil
	}

	seen := make(map[int]struct{}, len(picked))
	indices := make([]int, 0, len(picked))
	for _, problem := range picked {
		if problem < 1 || problem > total {
			return nil, fmt.Errorf("%s: 题号 %d 超出范围 1-%d", dataset, problem, total)
		}
		index := problem - 1
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		indices = append(indices, index)
	}

	sort.Ints(indices)
	return indices, nil
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

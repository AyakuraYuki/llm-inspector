package dataset

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"strconv"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
)

type aime26Row struct {
	Problem string `json:"problem"`
	Answer  int    `json:"answer"`
	ID      int    `json:"id"`
}

func (cfg *Config) aime26() ([]types.Question, error) {
	if !cfg.AIME26 {
		return make([]types.Question, 0), nil // skip
	}

	f, err := hfDatasetFS.Open("hf/math-ai/aime26/aime2026.jsonl")
	if err != nil {
		return nil, err
	}
	defer func(fileBytes fs.File) { _ = fileBytes.Close() }(f)

	scanner := bufio.NewScanner(f)
	problems := make([]aime26Row, 0)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row aime26Row
		if err = json.Unmarshal(line, &row); err != nil {
			return nil, err
		}
		problems = append(problems, row)
	}

	var questions []types.Question
	for _, r := range problems {
		questions = append(questions, types.Question{
			Dataset:  AIME26,
			Question: fmt.Sprintf("%s\n\nPlease reason step by step, and put your final answer within \\boxed{}.", r.Problem),
			Answer:   new(strconv.Itoa(r.Answer)),
		})
	}

	return questions, nil
}

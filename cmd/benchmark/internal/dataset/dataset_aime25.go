package dataset

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
)

type aime25Row struct {
	Problem string `json:"problem"`
	Answer  int    `json:"answer"`
	ID      string `json:"id"`
}

func (cfg *Config) aime25() ([]types.Question, error) {
	if !cfg.AIME25 {
		return make([]types.Question, 0), nil // skip
	}

	f, err := hfDatasetFS.Open("hf/math-ai/aime25/test.jsonl")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	problems := make([]aime25Row, 0)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row aime25Row
		if err = json.Unmarshal(line, &row); err != nil {
			return nil, err
		}
		problems = append(problems, row)
	}

	var questions []types.Question
	for _, r := range problems {
		questions = append(questions, types.Question{
			Dataset:  AIME25,
			Question: fmt.Sprintf("%s\n\nPlease reason step by step, and put your final answer within \\boxed{}.", r.Problem),
			Answer:   new(strconv.Itoa(r.Answer)),
		})
	}

	return questions, nil
}

package dataset

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"

	"github.com/parquet-go/parquet-go"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
)

type mmluProRow struct {
	QuestionID  int64    `parquet:"question_id"`
	Question    string   `parquet:"question"`
	Options     []string `parquet:"options,list"`
	Answer      string   `parquet:"answer"`
	AnswerIndex int64    `parquet:"answer_index"`
	CotContent  string   `parquet:"cot_content"`
	Category    string   `parquet:"category"`
	Src         string   `parquet:"src"`
}

type MMLUProConfig struct {
	Enabled         bool `yaml:"enabled"`
	UseValidation   bool `yaml:"use_validation"`
	UsePickup       bool `yaml:"use_pickup"`
	Biology         int  `yaml:"biology"`
	Business        int  `yaml:"business"`
	Chemistry       int  `yaml:"chemistry"`
	ComputerScience int  `yaml:"computer_science"`
	Economics       int  `yaml:"economics"`
	Engineering     int  `yaml:"engineering"`
	Health          int  `yaml:"health"`
	History         int  `yaml:"history"`
	Law             int  `yaml:"law"`
	Math            int  `yaml:"math"`
	Philosophy      int  `yaml:"philosophy"`
	Physics         int  `yaml:"physics"`
	Psychology      int  `yaml:"psychology"`
	Other           int  `yaml:"other"`
}

func (cfg MMLUProConfig) validations() ([]types.Question, error) {
	if cfg.Enabled && cfg.UseValidation {
		rows, err := cfg.loadDataset("hf/TIGER-Lab/MMLU-Pro/data/validation-00000-of-00001.parquet")
		if err != nil {
			return nil, err
		}
		return cfg.convertToQuestion(rows)
	}
	return make([]types.Question, 0), nil
}

func (cfg MMLUProConfig) allQuestions() ([]types.Question, error) {
	if cfg.Enabled {
		rows, err := cfg.loadDataset("hf/TIGER-Lab/MMLU-Pro/data/test-00000-of-00001.parquet")
		if err != nil {
			return nil, err
		}
		return cfg.convertToQuestion(rows)
	}
	return make([]types.Question, 0), nil
}

func (cfg MMLUProConfig) pickup() ([]types.Question, error) {
	if !cfg.Enabled {
		return make([]types.Question, 0), nil
	}
	if !cfg.UsePickup {
		return cfg.allQuestions() // fallback
	}

	rows, err := cfg.loadDataset("hf/TIGER-Lab/MMLU-Pro/data/test-00000-of-00001.parquet")
	if err != nil {
		return nil, err
	}

	var (
		grouped   = make(map[string][]mmluProRow)
		converted = make(map[string][]types.Question)
		pickup    = make([]types.Question, 0)
	)

	rand.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })

	for _, row := range rows {
		grouped[row.Category] = append(grouped[row.Category], row)
	}

	for category := range grouped {
		converted[category], err = cfg.convertToQuestion(grouped[category])
		if err != nil {
			return nil, err
		}
	}

	boundary := map[string]int{
		"biology":          min(cfg.Biology, len(grouped["biology"])),
		"business":         min(cfg.Business, len(grouped["business"])),
		"chemistry":        min(cfg.Chemistry, len(grouped["chemistry"])),
		"computer science": min(cfg.ComputerScience, len(grouped["computer science"])),
		"economics":        min(cfg.Economics, len(grouped["economics"])),
		"engineering":      min(cfg.Engineering, len(grouped["engineering"])),
		"health":           min(cfg.Health, len(grouped["health"])),
		"history":          min(cfg.History, len(grouped["history"])),
		"law":              min(cfg.Law, len(grouped["law"])),
		"math":             min(cfg.Math, len(grouped["math"])),
		"philosophy":       min(cfg.Philosophy, len(grouped["philosophy"])),
		"physics":          min(cfg.Physics, len(grouped["physics"])),
		"psychology":       min(cfg.Psychology, len(grouped["psychology"])),
		"other":            min(cfg.Other, len(grouped["other"])),
	}

	for category, limit := range boundary {
		if limit > 0 {
			pickup = append(pickup, converted[category][:limit]...)
		}
	}

	return pickup, nil
}

func (cfg MMLUProConfig) loadDataset(data string) ([]mmluProRow, error) {
	fileBytes, err := hfDatasetFS.ReadFile(data)
	if err != nil {
		return nil, err
	}

	fileReader := bytes.NewReader(fileBytes)
	fileSize := int64(len(fileBytes))

	pf, err := parquet.OpenFile(fileReader, fileSize)
	if err != nil {
		return nil, err
	}

	reader := parquet.NewGenericReader[mmluProRow](pf)
	defer func(reader *parquet.GenericReader[mmluProRow]) { _ = reader.Close() }(reader)

	rows := make([]mmluProRow, reader.NumRows())
	for {
		if _, errRead := reader.Read(rows); errRead != nil {
			if errors.Is(errRead, io.EOF) {
				break
			}
			return nil, errRead
		}
	}

	return rows, nil
}

func (cfg MMLUProConfig) convertToQuestion(rows []mmluProRow) ([]types.Question, error) {
	var questions []types.Question

	for _, row := range rows {
		optionLines := make([]string, 0)
		for i, option := range row.Options {
			optionLines = append(optionLines, fmt.Sprintf("(%c) %s", 'A'+i, option))
		}
		optionContent := strings.Join(optionLines, "\n")

		questionContent := fmt.Sprintf(`Answer the following multiple choice question about %s.

%s

%s

Think step by step, then give the letter of the correct option inside \boxed{}.
For example, if the answer is option C, end your response with \boxed{C}.
`, row.Category, row.Question, optionContent)

		questions = append(questions, types.Question{
			Dataset:  MMLUPro,
			Question: questionContent,
			Answer:   new(row.Answer),
		})
	}

	return questions, nil
}

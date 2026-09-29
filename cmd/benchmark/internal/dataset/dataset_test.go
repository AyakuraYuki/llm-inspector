package dataset

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

// aime25Answers / aime26Answers 是题库按题号顺序的答案表，供下面的用例对照。
var (
	aime25Answers = []int{
		70, 588, 16, 117, 279,
		504, 821, 77, 62, 81,
		259, 510, 204, 60, 735,
		468, 49, 82, 106, 336,
		293, 237, 610, 149, 907,
		113, 19, 248, 104, 240,
	}
	aime26Answers = []int{
		277, 62, 79, 70, 65,
		441, 396, 244, 29, 156,
		896, 161, 39, 681, 83,
		178, 243, 503, 279, 190,
		50, 754, 245, 669, 850,
		132, 223, 107, 157, 393,
	}
)

func Test_aime25(t *testing.T) {
	cfg := &Config{AIME25: true}

	questions, err := cfg.aime25()
	assert.NoError(t, err)
	assert.Len(t, questions, 30)

	for i, question := range questions {
		t.Run(fmt.Sprintf("problem_%d", i+1), func(t *testing.T) {
			assert.EqualValues(t, AIME25, question.Dataset)
			assert.EqualValues(t, strconv.Itoa(aime25Answers[i]), *question.Answer)
		})
	}
}

func Test_aime26(t *testing.T) {
	cfg := &Config{AIME26: true}

	questions, err := cfg.aime26()
	assert.NoError(t, err)
	assert.Len(t, questions, 30)

	for i, question := range questions {
		t.Run(fmt.Sprintf("problem_%d", i+1), func(t *testing.T) {
			assert.EqualValues(t, AIME26, question.Dataset)
			assert.EqualValues(t, strconv.Itoa(aime26Answers[i]), *question.Answer)
		})
	}
}

func Test_aime_problems(t *testing.T) {
	t.Run("aime25 picks the given problems in order", func(t *testing.T) {
		cfg := &Config{AIME25: true, AIME25Problems: []int{3, 1, 2}}

		questions, err := cfg.aime25()
		assert.NoError(t, err)
		assert.Len(t, questions, 3)

		// 题号升序，即使配置里乱序
		for i, problem := range []int{1, 2, 3} {
			assert.EqualValues(t, AIME25, questions[i].Dataset)
			assert.EqualValues(t, strconv.Itoa(aime25Answers[problem-1]), *questions[i].Answer)
		}
	})

	t.Run("aime26 picks the given problems", func(t *testing.T) {
		cfg := &Config{AIME26: true, AIME26Problems: []int{30, 7}}

		questions, err := cfg.aime26()
		assert.NoError(t, err)
		assert.Len(t, questions, 2)
		assert.EqualValues(t, strconv.Itoa(aime26Answers[6]), *questions[0].Answer)
		assert.EqualValues(t, strconv.Itoa(aime26Answers[29]), *questions[1].Answer)
	})

	t.Run("duplicated problems are collapsed", func(t *testing.T) {
		cfg := &Config{AIME25: true, AIME25Problems: []int{5, 5, 5}}

		questions, err := cfg.aime25()
		assert.NoError(t, err)
		assert.Len(t, questions, 1)
		assert.EqualValues(t, strconv.Itoa(aime25Answers[4]), *questions[0].Answer)
	})

	t.Run("empty problem list keeps the full set", func(t *testing.T) {
		cfg := &Config{AIME25: true}

		questions, err := cfg.aime25()
		assert.NoError(t, err)
		assert.Len(t, questions, 30)
	})

	t.Run("out of range problem is rejected", func(t *testing.T) {
		for _, problem := range []int{0, -1, 31} {
			cfg := &Config{AIME25: true, AIME25Problems: []int{problem}}

			questions, err := cfg.aime25()
			assert.ErrorContains(t, err, fmt.Sprintf("题号 %d 超出范围 1-30", problem))
			assert.Empty(t, questions)
		}
	})

	t.Run("disabled dataset ignores the problem list", func(t *testing.T) {
		cfg := &Config{AIME25: false, AIME25Problems: []int{999}}

		questions, err := cfg.aime25()
		assert.NoError(t, err)
		assert.Empty(t, questions)
	})
}

func Test_MMLUProConfig_questions(t *testing.T) {
	conf := MMLUProConfig{Enabled: true}
	questions, err := conf.allQuestions()
	assert.NoError(t, err)
	assert.Len(t, questions, 12032)

	conf.UseValidation = true
	// allow to use validation question set
	questions, err = conf.validations()
	assert.NoError(t, err)
	assert.Len(t, questions, 70)
	// allow to get full question set when enable use_validation
	questions, err = conf.allQuestions()
	assert.NoError(t, err)
	assert.Len(t, questions, 12032)
}

func Test_MMLUProConfig_pickup(t *testing.T) {
	conf := MMLUProConfig{
		Enabled:         true,
		UsePickup:       true,
		Biology:         49,
		Business:        33,
		Chemistry:       140,
		ComputerScience: 17,
		Economics:       35,
		Engineering:     40,
		Health:          34,
		History:         16,
		Law:             46,
		Math:            56,
		Philosophy:      21,
		Physics:         140,
		Psychology:      33,
		Other:           38,
	}

	questions, err := conf.pickup()
	assert.NoError(t, err)
	assert.Len(t, questions, 698)

	// allow to get full question set when enable use_pickup
	questions, err = conf.allQuestions()
	assert.NoError(t, err)
	assert.Len(t, questions, 12032)
}

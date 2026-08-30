package dataset

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_aime25(t *testing.T) {
	cfg := &Config{AIME25: true}

	questions, err := cfg.aime25()
	assert.NoError(t, err)
	assert.Len(t, questions, 30)

	answers := []int{
		70, 588, 16, 117, 279,
		504, 821, 77, 62, 81,
		259, 510, 204, 60, 735,
		468, 49, 82, 106, 336,
		293, 237, 610, 149, 907,
		113, 19, 248, 104, 240,
	}
	for i, question := range questions {
		t.Run(fmt.Sprintf("problem_%d", i+1), func(t *testing.T) {
			assert.EqualValues(t, AIME25, question.Dataset)
			assert.EqualValues(t, strconv.Itoa(answers[i]), *question.Answer)
		})
	}
}

func Test_aime26(t *testing.T) {
	cfg := &Config{AIME26: true}

	questions, err := cfg.aime26()
	assert.NoError(t, err)
	assert.Len(t, questions, 30)

	answers := []int{
		277, 62, 79, 70, 65,
		441, 396, 244, 29, 156,
		896, 161, 39, 681, 83,
		178, 243, 503, 279, 190,
		50, 754, 245, 669, 340,
		132, 223, 107, 157, 393,
	}
	for i, question := range questions {
		t.Run(fmt.Sprintf("problem_%d", i+1), func(t *testing.T) {
			assert.EqualValues(t, AIME26, question.Dataset)
			assert.EqualValues(t, strconv.Itoa(answers[i]), *question.Answer)
		})
	}
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

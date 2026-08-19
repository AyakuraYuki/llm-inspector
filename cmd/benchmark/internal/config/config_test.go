package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	configPath := filepath.Join("../../configs", "config.example.yml")
	conf, err := Load(configPath)
	assert.NoError(t, err)
	assert.True(t, len(conf.ExtraThinking) > 0)

	benchConf := conf.BenchmarkConfig()
	assert.True(t, len(benchConf.Thinking) > 0)
}

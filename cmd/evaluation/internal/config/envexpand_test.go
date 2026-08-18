package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadYAML 把内容写入临时文件后加载，返回 Load 的结果。
func loadYAML(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conf.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	defer func(name string) { _ = os.Remove(name) }(path)
	return Load(path, "test")
}

const minimalTarget = `
target:
  base_url: http://x/v1
  model: m
`

func TestExpandDefaultValue(t *testing.T) {
	_ = os.Unsetenv("EVAL_TEST_MODEL")
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: ${EVAL_TEST_MODEL:-gpt-5.4}
`)
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.4", conf.Target.Model)
}

func TestExpandEnvOverridesDefault(t *testing.T) {
	t.Setenv("EVAL_TEST_MODEL", "claude-opus-5")
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: ${EVAL_TEST_MODEL:-gpt-5.4}
`)
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-5", conf.Target.Model)
}

// `:-` 在变量为空时回落到默认值，`-` 则保留空字符串。
func TestExpandEmptyValueOperators(t *testing.T) {
	t.Setenv("EVAL_TEST_EMPTY", "")

	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: m
  api_key: ${EVAL_TEST_EMPTY:-fallback}
`)
	require.NoError(t, err)
	assert.Equal(t, "fallback", conf.Target.APIKey)

	conf, err = loadYAML(t, `
target:
  base_url: http://x/v1
  model: m
  api_key: ${EVAL_TEST_EMPTY-fallback}
`)
	require.NoError(t, err)
	assert.Empty(t, conf.Target.APIKey)
}

func TestExpandRequiredMissing(t *testing.T) {
	_ = os.Unsetenv("EVAL_TEST_ABSENT")
	_, err := loadYAML(t, strings.TrimLeft(`
target:
  base_url: http://x/v1
  model: ${EVAL_TEST_ABSENT}
`, "\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EVAL_TEST_ABSENT")
	assert.Contains(t, err.Error(), "conf.yml:3:10", "错误应带上行列位置")
}

func TestExpandRequiredCustomMessage(t *testing.T) {
	_ = os.Unsetenv("EVAL_TEST_KEY")
	_, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: m
  api_key: ${EVAL_TEST_KEY:?请设置 API key}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "请设置 API key")
}

// 多个缺失变量应一次性全部报出，而不是遇到第一个就停。
func TestExpandAggregatesAllErrors(t *testing.T) {
	_ = os.Unsetenv("EVAL_TEST_A")
	_ = os.Unsetenv("EVAL_TEST_B")
	_, err := loadYAML(t, `
target:
  base_url: ${EVAL_TEST_A}
  model: ${EVAL_TEST_B}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EVAL_TEST_A")
	assert.Contains(t, err.Error(), "EVAL_TEST_B")
}

// 未设置时报错信息不得回显任何已展开的值，避免密钥进日志。
func TestExpandErrorDoesNotLeakValues(t *testing.T) {
	t.Setenv("EVAL_TEST_SECRET", "sk-super-secret")
	_ = os.Unsetenv("EVAL_TEST_ABSENT")
	_, err := loadYAML(t, `
target:
  base_url: http://x/v1
  api_key: ${EVAL_TEST_SECRET}
  model: ${EVAL_TEST_ABSENT}
`)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sk-super-secret")
}

// 无引号标量展开后应重新推断类型，否则 int/float/bool 字段会解码失败。
func TestExpandRetainsScalarTypes(t *testing.T) {
	_ = os.Unsetenv("EVAL_TEST_RUNS")
	t.Setenv("EVAL_TEST_SCORE", "0.95")
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: m
layers:
  performance:
    enabled: ${EVAL_TEST_ENABLED:-true}
    runs: ${EVAL_TEST_RUNS:-42}
thresholds:
  min_layer_score: ${EVAL_TEST_SCORE}
`)
	require.NoError(t, err)
	assert.Equal(t, 42, conf.Layers.Performance.Runs)
	assert.Equal(t, 0.95, conf.Thresholds.MinLayerScore)
	require.NotNil(t, conf.Layers.Performance.Enabled)
	assert.True(t, *conf.Layers.Performance.Enabled)
}

// 带引号的标量保持字符串，即让值看起来像数字。
func TestExpandQuotedStaysString(t *testing.T) {
	t.Setenv("EVAL_TEST_MODEL", "123")
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: "${EVAL_TEST_MODEL}"
`)
	require.NoError(t, err)
	assert.Equal(t, "123", conf.Target.Model)
}

// 默认值中的 } 不应被提前截断。
func TestExpandDefaultContainingBraces(t *testing.T) {
	_ = os.Unsetenv("EVAL_TEST_JSON")
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: ${EVAL_TEST_JSON:-{"a":1}}
`)
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, conf.Target.Model)
}

func TestExpandNestedDefault(t *testing.T) {
	_ = os.Unsetenv("EVAL_TEST_OUTER")
	t.Setenv("EVAL_TEST_INNER", "inner-model")
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: ${EVAL_TEST_OUTER:-${EVAL_TEST_INNER}}
`)
	require.NoError(t, err)
	assert.Equal(t, "inner-model", conf.Target.Model)
}

// $$ 转义为字面 $，裸 $VAR 原样保留。
func TestExpandEscapeAndBareDollar(t *testing.T) {
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: "a$$b $HOME/c"
`)
	require.NoError(t, err)
	assert.Equal(t, "a$b $HOME/c", conf.Target.Model)
}

// 注释里的 $ 不参与展开——这是节点级展开相对文本替换的关键优势。
func TestExpandIgnoresComments(t *testing.T) {
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1  # 例如 ${EVAL_TEST_NOPE}
  model: m
`)
	require.NoError(t, err)
	assert.Equal(t, "http://x/v1", conf.Target.BaseURL)
}

// 环境变量的值始终是字面标量，不会被解释为 YAML 结构。
func TestExpandValueCannotInjectYAML(t *testing.T) {
	t.Setenv("EVAL_TEST_INJECT", "m\napi_key: injected")
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: ${EVAL_TEST_INJECT}
`)
	require.NoError(t, err)
	assert.Equal(t, "m\napi_key: injected", conf.Target.Model)
	assert.Empty(t, conf.Target.APIKey)
}

func TestExpandUnclosedReference(t *testing.T) {
	_, err := loadYAML(t, `
target:
  base_url: http://x/v1
  model: "${EVAL_TEST_X"
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未闭合")
}

// 空文档不应 panic。
func TestLoadEmptyDocument(t *testing.T) {
	for name, content := range map[string]string{
		"空文件":  "",
		"纯注释":  "# 只有注释\n",
		"文档标记": "---\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadYAML(t, content)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "为空")
		})
	}
}

// 不含 $ 的配置行为与展开前完全一致。
func TestLoadWithoutInterpolation(t *testing.T) {
	conf, err := loadYAML(t, minimalTarget)
	require.NoError(t, err)
	assert.Equal(t, "http://x/v1", conf.Target.BaseURL)
	assert.Equal(t, 5, conf.Layers.Stability.Samples)
}

func TestExpandDepthLimit(t *testing.T) {
	_ = os.Unsetenv("EVAL_TEST_LOOP")
	ref := "${EVAL_TEST_LOOP:-x}"
	for range maxExpandDepth + 2 {
		ref = "${EVAL_TEST_LOOP:-" + ref + "}"
	}
	_, err := loadYAML(t, fmt.Sprintf(`
target:
  base_url: http://x/v1
  model: "%s"
`, ref))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "嵌套展开超过")
}

// 展开发生在标量层，key 同样会被展开。
func TestExpandAppliesToKeys(t *testing.T) {
	t.Setenv("EVAL_TEST_KEYNAME", "model")
	conf, err := loadYAML(t, `
target:
  base_url: http://x/v1
  ${EVAL_TEST_KEYNAME}: from-key
`)
	require.NoError(t, err)
	assert.Equal(t, "from-key", conf.Target.Model)
}

func TestInferTagKeepsMultilineAsString(t *testing.T) {
	assert.Equal(t, strTag, inferTag("a: b\nc: d"))
	assert.Equal(t, strTag, inferTag("[1, 2]"))
	assert.Equal(t, strTag, inferTag("'quoted'"))
	assert.Equal(t, intTag, inferTag("42"))
	assert.Equal(t, boolTag, inferTag("true"))
	assert.Equal(t, strTag, inferTag("plain text"))
}

func TestSplitName(t *testing.T) {
	cases := []struct{ ref, name, rest string }{
		{"VAR", "VAR", ""},
		{"VAR:-def", "VAR", ":-def"},
		{"VAR-def", "VAR", "-def"},
		{"VAR:?msg", "VAR", ":?msg"},
		{"_V1:-x", "_V1", ":-x"},
		{"1VAR", "", "1VAR"},
		{"", "", ""},
	}
	for _, c := range cases {
		t.Run(c.ref, func(t *testing.T) {
			name, rest := splitName(c.ref)
			assert.Equal(t, c.name, name, "ref=%q", c.ref)
			assert.Equal(t, c.rest, rest, "ref=%q", c.ref)
		})
	}
}

func TestExpandInvalidReference(t *testing.T) {
	for _, ref := range []string{"${}", "${1BAD}", "${VAR+other}"} {
		t.Run(ref, func(t *testing.T) {
			_, err := loadYAML(t, fmt.Sprintf(`
target:
  base_url: http://x/v1
  model: "%s"
`, ref))
			require.Error(t, err, "ref=%s", ref)
			assert.True(t, strings.Contains(err.Error(), "无效的变量引用"), "ref=%s, err=%v", ref, err)
		})
	}
}

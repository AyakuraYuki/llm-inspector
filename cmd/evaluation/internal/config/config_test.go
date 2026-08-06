package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "configs", "eval.example.yml"), filepath.Base(os.Args[0]))
	if err != nil {
		t.Fatalf("加载示例配置失败: %v", err)
	}
	if cfg.Target.BaseURL == "" || cfg.Target.Model == "" {
		t.Fatal("target 字段缺失")
	}
	// 默认值
	if cfg.Layers.Stability.Samples != 5 {
		t.Fatalf("Samples 默认值 = %d, want 5", cfg.Layers.Stability.Samples)
	}
	if cfg.Layers.Performance.Runs != 20 {
		t.Fatalf("Runs 默认值 = %d, want 20", cfg.Layers.Performance.Runs)
	}
	if cfg.Thresholds.MinLayerScore != 0.8 {
		t.Fatalf("MinLayerScore = %v, want 0.8", cfg.Thresholds.MinLayerScore)
	}
	// 未显式写 enabled 的层默认启用
	if !Enabled(cfg.Layers.Availability.Enabled) {
		t.Fatal("availability 应默认启用")
	}
}

func TestLoadDefaultsOnMinimal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "min.yaml")
	content := "target:\n  base_url: http://x/v1\n  model: m\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, filepath.Base(os.Args[0]))
	if err != nil {
		t.Fatalf("加载最小配置失败: %v", err)
	}
	d, err := cfg.Target.TimeoutDuration()
	if err != nil || d.Seconds() != 60 {
		t.Fatalf("默认 timeout = %v, err %v", d, err)
	}
	if len(cfg.Layers.Performance.Concurrency) != 3 {
		t.Fatalf("默认并发梯度 = %v", cfg.Layers.Performance.Concurrency)
	}
}

func TestLoadInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("target:\n  base_url: ''\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, filepath.Base(os.Args[0])); err == nil {
		t.Fatal("缺少 base_url 应报错")
	}
}

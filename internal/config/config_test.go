package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRerankKeyFallbackToDashScope 验证：DASHSCOPE_API_KEY 无条件回落到 Rerank.APIKey
// （viper 不展开 ${} 占位符，必须靠 env 覆盖；RERANK_API_KEY 优先级更高）。
func TestRerankKeyFallbackToDashScope(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "dashscope-secret")
	os.Unsetenv("RERANK_API_KEY")

	cfg, err := Load(filepath.Join("..", "..", "configs", "config.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Rerank.APIKey != "dashscope-secret" {
		t.Errorf("Rerank.APIKey = %q, want fallback to DASHSCOPE_API_KEY", cfg.Rerank.APIKey)
	}
	if !cfg.Rerank.Enabled {
		t.Error("rerank should be enabled by default in config.yaml")
	}
}

// TestRerankKeyEnvOverride 验证：RERANK_API_KEY 显式设置时优先于 DASHSCOPE_API_KEY。
func TestRerankKeyEnvOverride(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "dashscope-secret")
	t.Setenv("RERANK_API_KEY", "rerank-secret")

	cfg, err := Load(filepath.Join("..", "..", "configs", "config.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Rerank.APIKey != "rerank-secret" {
		t.Errorf("Rerank.APIKey = %q, want explicit RERANK_API_KEY override", cfg.Rerank.APIKey)
	}
}

// TestRerankEnvDisable 验证：RERANK_ENABLED=false 可关闭精排。
func TestRerankEnvDisable(t *testing.T) {
	t.Setenv("RERANK_ENABLED", "false")
	cfg, err := Load(filepath.Join("..", "..", "configs", "config.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Rerank.Enabled {
		t.Error("rerank should be disabled when RERANK_ENABLED=false")
	}
}

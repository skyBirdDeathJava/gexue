// Package config 使用 Viper 加载配置。
// 优先级：环境变量 > config.yaml > 默认值。
// 所有大模型配置项与 ~/ww/mianba/backend/config.py 对齐（环境变量名一致）。
package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	Server    Server
	DB        DB
	LLM       LLM
	Embedding Embedding
	Auth      Auth
	Chunk     Chunk
	Log       Log
}

type Server struct {
	Port int
}

type DB struct {
	DSN string
}

// Auth 认证（参考 mianba：JWT_SECRET_KEY，HS256，默认 7 天）
type Auth struct {
	JWTSecret     string `mapstructure:"jwt_secret"`
	TokenTTLHours int    `mapstructure:"token_ttl_hours"`
}

// Chunk 分块参数（见 KNOWLEDGE-BASE-SOLUTION §5）
type Chunk struct {
	MaxChunk int
	Overlap  int
}

// LLM 与 mianba config.py 对齐：方舟 + DeepSeek V4 直连 + DashScope
type LLM struct {
	ARKAPIKey  string `mapstructure:"ark_api_key"`
	ARKBaseURL string `mapstructure:"ark_base_url"`
	ARKModel   string `mapstructure:"ark_model"`

	DeepSeekV4APIKey  string `mapstructure:"deepseek_v4_api_key"`
	DeepSeekV4BaseURL string `mapstructure:"deepseek_v4_base_url"`
	DeepSeekV4Model   string `mapstructure:"deepseek_v4_model"`

	DashScopeAPIKey  string `mapstructure:"dashscope_api_key"`
	DashScopeBaseURL string `mapstructure:"dashscope_base_url"`
}

type Embedding struct {
	Provider string // dashscope / ark / local_bge
	Model    string
	Dim      int
	APIKey   string `mapstructure:"api_key"`
	BaseURL  string `mapstructure:"base_url"`
}

type Log struct {
	Level string
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	// 环境变量注入（与 mianba config.py 同名）
	bindEnv(v, "llm.ark_api_key", "ARK_API_KEY")
	bindEnv(v, "llm.deepseek_v4_api_key", "DEEPSEEK_V4_API_KEY")
	bindEnv(v, "llm.dashscope_api_key", "DASHSCOPE_API_KEY")
	bindEnv(v, "embedding.api_key", "DASHSCOPE_API_KEY")
	bindEnv(v, "auth.jwt_secret", "JWT_SECRET_KEY")
	bindEnv(v, "db.dsn", "DB_DSN")

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	// 默认值兜底
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Embedding.Dim == 0 {
		cfg.Embedding.Dim = 512
	}
	if cfg.Auth.TokenTTLHours == 0 {
		cfg.Auth.TokenTTLHours = 168 // 7 天，对齐 mianba ACCESS_TOKEN_EXPIRE_MINUTES
	}
	if cfg.Chunk.MaxChunk == 0 {
		cfg.Chunk.MaxChunk = 500
	}
	if cfg.Chunk.Overlap == 0 {
		cfg.Chunk.Overlap = 60
	}
	return &cfg, nil
}

func bindEnv(v *viper.Viper, key, env string) {
	_ = v.BindEnv(key, env)
}

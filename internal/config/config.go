// Package config 使用 Viper 加载配置。
// 优先级：环境变量 > config.yaml > 默认值。
// 所有大模型配置项与 ~/ww/mianba/backend/config.py 对齐（环境变量名一致）。
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/viper"
)

type Config struct {
	Server     Server
	DB         DB
	LLM        LLM
	Embedding  Embedding
	Rerank     Rerank
	Auth       Auth
	Chunk      Chunk
	Log        Log
	OSS        OSS
	VolcEngine VolcEngine
	AliyunSMS  AliyunSMS
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

// Rerank 精排配置。默认 DashScope qwen3-rerank（gte-rerank-v2 已于 2026-05-30 停服）。
// Enabled=false 或 APIKey 为空时跳过精排，仅返回 RRF 融合结果。
type Rerank struct {
	Enabled  bool   `mapstructure:"enabled"`
	Provider string // dashscope
	Model    string
	APIKey   string `mapstructure:"api_key"`
	BaseURL  string `mapstructure:"base_url"`
	TopN     int    `mapstructure:"top_n"` // 送精排候选数
}

// Log 日志配置。Level: debug/info/warn/error；File: 日志文件路径（空则仅控制台）。
type Log struct {
	Level string
	File  string
}

// OSS 阿里云对象存储配置（与 mianba config.py 对齐）
type OSS struct {
	AccessKeyID     string `mapstructure:"access_key_id"`
	AccessKeySecret string `mapstructure:"access_key_secret"`
	Bucket          string
	Endpoint        string
	AccelEndpoint   string `mapstructure:"accel_endpoint"`
}

// VolcEngine 火山引擎视觉服务配置（用于 OCR）
type VolcEngine struct {
	VisualAK string `mapstructure:"visual_ak"`
	VisualSK string `mapstructure:"visual_sk"`
}

// AliyunSMS 阿里云短信配置（可选；留空则用开发模式）
type AliyunSMS struct {
	AccessKeyID     string `mapstructure:"access_key_id"`
	AccessKeySecret string `mapstructure:"access_key_secret"`
	SignName        string `mapstructure:"sign_name"`
	TemplateCode    string `mapstructure:"template_code"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

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
	if cfg.Rerank.Provider == "" {
		cfg.Rerank.Provider = "dashscope"
	}
	if cfg.Rerank.Model == "" {
		cfg.Rerank.Model = "qwen3-rerank"
	}
	if cfg.Rerank.BaseURL == "" {
		cfg.Rerank.BaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
	}
	if cfg.Rerank.TopN == 0 {
		cfg.Rerank.TopN = 20
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

	// 环境变量覆盖（优先级高于配置文件）
	overrideFromEnv(&cfg)

	return &cfg, nil
}

// overrideFromEnv 使用环境变量覆盖配置
func overrideFromEnv(cfg *Config) {
	if v := os.Getenv("ARK_API_KEY"); v != "" {
		cfg.LLM.ARKAPIKey = v
	}
	if v := os.Getenv("DEEPSEEK_V4_API_KEY"); v != "" {
		cfg.LLM.DeepSeekV4APIKey = v
	}
	if v := os.Getenv("DASHSCOPE_API_KEY"); v != "" {
		cfg.LLM.DashScopeAPIKey = v
		cfg.Embedding.APIKey = v
		// rerank 默认复用同一把 DashScope key（viper 不展开 ${} 占位符，须无条件覆盖；
		// 如需独立 key 由下方 RERANK_API_KEY 再覆盖）
		cfg.Rerank.APIKey = v
	}
	// 重排（rerank）配置覆盖
	if v := os.Getenv("RERANK_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Rerank.Enabled = b
		}
	}
	if v := os.Getenv("RERANK_MODEL"); v != "" {
		cfg.Rerank.Model = v
	}
	if v := os.Getenv("RERANK_API_KEY"); v != "" {
		cfg.Rerank.APIKey = v
	}
	if v := os.Getenv("RERANK_BASE_URL"); v != "" {
		cfg.Rerank.BaseURL = v
	}
	if v := os.Getenv("RERANK_TOP_N"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Rerank.TopN = n
		}
	}
	if v := os.Getenv("JWT_SECRET_KEY"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("DB_DSN"); v != "" {
		cfg.DB.DSN = v
	}
	if v := os.Getenv("LOG_FILE"); v != "" {
		cfg.Log.File = v
	}
	if v := os.Getenv("OSS_ACCESS_KEY_ID"); v != "" {
		cfg.OSS.AccessKeyID = v
	}
	if v := os.Getenv("OSS_ACCESS_KEY_SECRET"); v != "" {
		cfg.OSS.AccessKeySecret = v
	}
	if v := os.Getenv("OSS_BUCKET"); v != "" {
		cfg.OSS.Bucket = v
	}
	if v := os.Getenv("OSS_ENDPOINT"); v != "" {
		cfg.OSS.Endpoint = v
	}
	if v := os.Getenv("OSS_ACCEL_ENDPOINT"); v != "" {
		cfg.OSS.AccelEndpoint = v
	}
	if v := os.Getenv("VOLCENGINE_VISUAL_AK"); v != "" {
		cfg.VolcEngine.VisualAK = v
	}
	if v := os.Getenv("VOLCENGINE_VISUAL_SK"); v != "" {
		cfg.VolcEngine.VisualSK = v
	}
	// 阿里云短信配置
	if v := os.Getenv("ALIYUN_ACCESS_KEY_ID"); v != "" {
		cfg.AliyunSMS.AccessKeyID = v
	}
	if v := os.Getenv("ALIYUN_ACCESS_KEY_SECRET"); v != "" {
		cfg.AliyunSMS.AccessKeySecret = v
	}
	if v := os.Getenv("ALIYUN_SMS_SIGN_NAME"); v != "" {
		cfg.AliyunSMS.SignName = v
	}
	if v := os.Getenv("ALIYUN_SMS_TEMPLATE_CODE"); v != "" {
		cfg.AliyunSMS.TemplateCode = v
	}
}
func bindEnv(v *viper.Viper, key, env string) {
	_ = v.BindEnv(key, env)
}

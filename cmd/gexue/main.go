// 个学（GeXue）入口。
// 装配：配置 → zap 日志 → PostgreSQL(pgvector) → AutoMigrate+HNSW → 认证/知识库 service → Gin 路由。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"gexue/internal/agent"
	"gexue/internal/api"
	"gexue/internal/config"
	"gexue/internal/embedding"
	"gexue/internal/metrics"
	"gexue/internal/model"
	"gexue/internal/pkg/oss"
	"gexue/internal/pkg/sms"
	"gexue/internal/pkg/volcengine"
	"gexue/internal/repo"
	"gexue/internal/rerank"
	"gexue/internal/retrieval"
	"gexue/internal/route"
	"gexue/internal/service"
)

func main() {
	// 本地 .env（变量名与 mianba 对齐），不存在则忽略
	// 支持多个路径：当前目录、项目根目录
	_ = godotenv.Load()
	_ = godotenv.Load("../../.env")   // 如果从 cmd/gexue/ 目录运行
	_ = godotenv.Load("configs/.env") // 配置目录备选

	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logger := initLogger(cfg)
	defer logger.Sync()

	db := initDB(cfg, logger)

	// ---- 认证 ----
	userRepo := repo.NewUserRepo(db)

	// SMS 提供者：优先使用阿里云
	var smsProvider sms.Provider
	if cfg.AliyunSMS.AccessKeyID != "" && cfg.AliyunSMS.AccessKeySecret != "" && cfg.AliyunSMS.SignName != "" && cfg.AliyunSMS.TemplateCode != "" {
		aliyunProvider, err := sms.NewAliyunProvider(
			cfg.AliyunSMS.AccessKeyID,
			cfg.AliyunSMS.AccessKeySecret,
			cfg.AliyunSMS.SignName,
			cfg.AliyunSMS.TemplateCode,
		)
		if err != nil {
			logger.Error("init aliyun sms provider failed, fallback to local", zap.Error(err))
			smsProvider = sms.NewLocalProvider()
		} else {
			smsProvider = aliyunProvider
			logger.Info("aliyun sms provider initialized")
		}
	} else {
		smsProvider = sms.NewLocalProvider()
		logger.Warn("using local sms provider (development mode)")
	}

	authSvc := service.NewAuthService(userRepo, cfg.Auth.JWTSecret, time.Duration(cfg.Auth.TokenTTLHours)*time.Hour, smsProvider)
	authHandler := api.NewAuthHandler(authSvc)

	// ---- 知识库 ----
	emb := embedding.NewDashScope(cfg.Embedding.APIKey, cfg.Embedding.Model, cfg.Embedding.Dim)
	kbRepo := repo.NewKnowledgeRepo(db)

	// rerank 精排（可选）：未启用/无 key 时跳过精排，仅返回 RRF 融合结果
	var reranker rerank.Reranker
	if cfg.Rerank.Enabled && cfg.Rerank.APIKey != "" {
		reranker = rerank.NewDashScopeWithURL(cfg.Rerank.APIKey, cfg.Rerank.Model, cfg.Rerank.BaseURL)
		logger.Info("rerank enabled",
			zap.String("model", cfg.Rerank.Model), zap.Int("top_n", cfg.Rerank.TopN))
	} else {
		logger.Warn("rerank disabled, will fall back to rrf fusion result",
			zap.Bool("enabled", cfg.Rerank.Enabled), zap.Bool("has_key", cfg.Rerank.APIKey != ""))
	}

	// 双路召回检索编排：向量 + BM25 → RRF 融合 → rerank 精排
	// 注入 Prometheus 检索链路指标（/metrics 暴露，Grafana 观测）
	retriever := retrieval.NewRetriever(kbRepo, emb, reranker, logger,
		retrieval.WithRerankTopN(cfg.Rerank.TopN),
		retrieval.WithMetrics(metrics.NewRetrievalMetrics()))

	kbSvc := service.NewKnowledgeService(kbRepo, emb, cfg.Chunk.MaxChunk, cfg.Chunk.Overlap)
	kbSvc.SetRetriever(retriever)

	// 初始化火山引擎 OCR 客户端（可选，知识库与答题图片共用）
	var ocrClient *volcengine.OCRClient
	if cfg.VolcEngine.VisualAK != "" && cfg.VolcEngine.VisualSK != "" {
		ocrClient = volcengine.NewOCRClient(cfg.VolcEngine.VisualAK, cfg.VolcEngine.VisualSK)
		kbSvc.SetOCRClient(ocrClient)
		logger.Info("volcengine ocr client initialized")
	} else {
		logger.Warn("volcengine ocr credentials not configured, image/pdf ocr disabled")
	}

	// 初始化 OSS（可选）
	if cfg.OSS.AccessKeyID != "" && cfg.OSS.Bucket != "" {
		ossUploader, err := oss.NewUploader(oss.Config{
			AccessKeyID:     cfg.OSS.AccessKeyID,
			AccessKeySecret: cfg.OSS.AccessKeySecret,
			Bucket:          cfg.OSS.Bucket,
			Endpoint:        cfg.OSS.Endpoint,
			AccelEndpoint:   cfg.OSS.AccelEndpoint,
		})
		if err != nil {
			logger.Warn("init oss uploader failed", zap.Error(err))
		} else {
			// 注入 OSS 上传函数：上传原始文件到 OSS
			// objectKey: 原始文件的对象键，filePath: 本地临时路径
			kbSvc.SetOSSUpload(func(objectKey, filePath string) (string, error) {
				// 生成 OSS 对象键：documents/{timestamp}_{fileName}
				ossKey := oss.GenerateObjectKey(0, objectKey)
				return ossUploader.Upload(ossKey, filePath)
			})
			logger.Info("oss uploader initialized")
		}
	}

	kbHandler := api.NewKnowledgeHandler(kbSvc)

	// ---- 模拟测试 Agent ----
	quizRepo := repo.NewQuizRepo(db)
	quizAgent, err := agent.NewAgent(quizRepo, kbRepo, emb, retriever, agent.LLMConfig{
		Provider: "deepseek",
		APIKey:   cfg.LLM.DeepSeekV4APIKey,
		BaseURL:  cfg.LLM.DeepSeekV4BaseURL,
		Model:    cfg.LLM.DeepSeekV4Model,
	}, logger)
	if err != nil {
		logger.Fatal("init agent failed", zap.Error(err))
	}

	// 模拟测试服务（注入 OSS 上传器，如果已配置）
	var ossUploader *oss.Uploader
	if cfg.OSS.AccessKeyID != "" && cfg.OSS.Bucket != "" {
		var err error
		ossUploader, err = oss.NewUploader(oss.Config{
			AccessKeyID:     cfg.OSS.AccessKeyID,
			AccessKeySecret: cfg.OSS.AccessKeySecret,
			Bucket:          cfg.OSS.Bucket,
			Endpoint:        cfg.OSS.Endpoint,
			AccelEndpoint:   cfg.OSS.AccelEndpoint,
		})
		if err != nil {
			logger.Warn("init oss uploader for quiz failed", zap.Error(err))
		} else {
			logger.Info("oss uploader for quiz initialized")
		}
	}

	quizSvc := service.NewQuizService(quizRepo, kbRepo, quizAgent, ossUploader)
	if ocrClient != nil {
		quizSvc.SetOCRClient(ocrClient)
	}
	quizHandler := api.NewQuizHandler(quizSvc)

	router := route.NewRouter(logger, route.Deps{
		Auth:      authHandler,
		Knowledge: kbHandler,
		Quiz:      quizHandler,
		JWTSecret: cfg.Auth.JWTSecret,
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("gexue server starting", zap.String("addr", addr))
	if err := router.Engine().Run(addr); err != nil {
		logger.Fatal("server exited", zap.Error(err))
	}
}

// initLogger 构建 zap logger。
// 输出：
//   - 控制台始终输出（stdout）；
//   - 配置了 log.file 时同时追加写文件（自动创建目录；文件打开失败仅告警，降级为纯控制台）。
//
// 编码：debug 级别用人类可读的 Console 编码，其余用 JSON（对齐 zap.NewDevelopment/NewProduction）。
func initLogger(cfg *config.Config) *zap.Logger {
	// 输出目标：默认仅控制台；配置 log.file 时控制台 + 文件双写
	ws := zapcore.Lock(os.Stdout)
	if cfg.Log.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Log.File), 0o755); err != nil {
			log.Printf("create log dir %s failed, keep stdout only: %v", cfg.Log.File, err)
		} else if f, err := os.OpenFile(cfg.Log.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
			log.Printf("open log file %s failed, keep stdout only: %v", cfg.Log.File, err)
		} else {
			ws = zapcore.NewMultiWriteSyncer(zapcore.AddSync(f), zapcore.Lock(os.Stdout))
		}
	}

	// 编码器与级别：对齐 zap.NewDevelopment（debug）/ NewProduction（info）
	var enc zapcore.Encoder
	var level zapcore.Level
	if cfg.Log.Level == "debug" {
		enc = zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
		level = zapcore.DebugLevel
	} else {
		enc = zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
		level = zapcore.InfoLevel
	}

	return zap.New(zapcore.NewCore(enc, ws, level), zap.AddCaller())
}

func initDB(cfg *config.Config, logger *zap.Logger) *gorm.DB {
	// DSN 默认本地 Docker Compose PG（见 docker-compose.yml）
	dsn := cfg.DB.DSN
	if dsn == "" {
		dsn = "postgres://gexue:gexue_password@localhost:5432/gexue?sslmode=disable&client_encoding=UTF8"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		logger.Fatal("connect db failed", zap.Error(err), zap.String("dsn", dsn))
	}

	// AutoMigrate 在这里被跳过，因为 init-db.sh 已经创建了所有表。
	// 如果希望 GORM 自动管理表结构，可以删除 init-db.sh 的 SQL 初始化部分
	// 并取消注释下面的 AutoMigrate 代码（需要解决约束冲突问题）。
	/*
		if err := db.AutoMigrate(
			&model.User{},
			&model.Grade{},
			&model.Subject{},
			&model.KnowledgeBase{},
			&model.KnowledgePoint{},
			&model.KnowledgeChunk{},
			&model.PracticeSession{},
			&model.Question{},
			&model.AnswerRecord{},
			&model.ChatTurn{},
		); err != nil {
			logger.Fatal("auto migrate failed", zap.Error(err))
		}
	*/

	// HNSW 索引 AutoMigrate 不会建，须显式执行（幂等）
	if err := db.Exec(
		"CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_vec " +
			"ON knowledge_chunks USING hnsw (content_vec vector_cosine_ops)",
	).Error; err != nil {
		logger.Warn("create hnsw index failed", zap.Error(err))
	}

	// 插入种子数据
	seedBasics(db)

	logger.Info("database ready")
	return db
}

// seedBasics 预置 年级表（1-6）+ 学科表，仅当表为空时写入（幂等）。
func seedBasics(db *gorm.DB) {
	var gradeCount int64
	db.Model(&model.Grade{}).Count(&gradeCount)
	if gradeCount == 0 {
		grades := make([]model.Grade, 0, 6)
		for i := 1; i <= 6; i++ {
			grades = append(grades, model.Grade{Code: i, Name: fmt.Sprintf("%d年级", i)})
		}
		if err := db.Create(&grades).Error; err != nil {
			log.Printf("seed grades failed: %v", err)
		}
	}
	var subjCount int64
	db.Model(&model.Subject{}).Count(&subjCount)
	if subjCount == 0 {
		subjects := []model.Subject{
			{Code: "yuwen", Name: "语文"},
			{Code: "math", Name: "数学"},
			{Code: "english", Name: "英语"},
		}
		if err := db.Create(&subjects).Error; err != nil {
			log.Printf("seed subjects failed: %v", err)
		}
	}
}

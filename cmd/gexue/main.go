// 个学（GeXue）入口。
// 装配：配置 → zap 日志 → PostgreSQL(pgvector) → AutoMigrate+HNSW → 认证/知识库 service → Gin 路由。
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"gexue/internal/api"
	"gexue/internal/config"
	"gexue/internal/embedding"
	"gexue/internal/model"
	"gexue/internal/repo"
	"gexue/internal/service"
)

func main() {
	// 本地 .env（变量名与 mianba 对齐），不存在则忽略
	_ = godotenv.Load()

	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logger := initLogger(cfg)
	defer logger.Sync()

	db := initDB(cfg, logger)

	// ---- 认证 ----
	userRepo := repo.NewUserRepo(db)
	authSvc := service.NewAuthService(userRepo, cfg.Auth.JWTSecret, time.Duration(cfg.Auth.TokenTTLHours)*time.Hour)
	authHandler := api.NewAuthHandler(authSvc)

	// ---- 知识库 ----
	emb := embedding.NewDashScope(cfg.Embedding.APIKey, cfg.Embedding.Model, cfg.Embedding.Dim)
	kbRepo := repo.NewKnowledgeRepo(db)
	kbSvc := service.NewKnowledgeService(kbRepo, emb, cfg.Chunk.MaxChunk, cfg.Chunk.Overlap)
	kbHandler := api.NewKnowledgeHandler(kbSvc)

	router := api.NewRouter(logger, api.Deps{
		Auth:      authHandler,
		Knowledge: kbHandler,
		JWTSecret: cfg.Auth.JWTSecret,
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("gexue server starting", zap.String("addr", addr))
	if err := router.Engine().Run(addr); err != nil {
		logger.Fatal("server exited", zap.Error(err))
	}
}

func initLogger(cfg *config.Config) *zap.Logger {
	if cfg.Log.Level == "debug" {
		logger, _ := zap.NewDevelopment()
		return logger
	}
	logger, _ := zap.NewProduction()
	return logger
}

func initDB(cfg *config.Config, logger *zap.Logger) *gorm.DB {
	// DSN 默认本地 Docker Compose PG（见 docker-compose.yml）
	dsn := cfg.DB.DSN
	if dsn == "" {
		dsn = "postgres://gexue:gexue@localhost:5432/gexue?sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		logger.Fatal("connect db failed", zap.Error(err), zap.String("dsn", dsn))
	}

	// AutoMigrate 建表（向量列维度 = model.EmbeddingDim）
	if err := db.AutoMigrate(
		&model.User{},
		&model.Grade{},
		&model.Subject{},
		&model.KnowledgeBase{},
		&model.KnowledgePoint{},
		&model.KnowledgeChunk{},
	); err != nil {
		logger.Fatal("auto migrate failed", zap.Error(err))
	}

	// HNSW 索引 AutoMigrate 不会建，须显式执行（幂等）
	if err := db.Exec(
		"CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_vec " +
			"ON knowledge_chunks USING hnsw (content_vec vector_cosine_ops)",
	).Error; err != nil {
		logger.Warn("create hnsw index failed", zap.Error(err))
	}

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

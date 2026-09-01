//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"gexue/internal/config"
	"gexue/internal/embedding"
	"gexue/internal/repo"
	"gexue/internal/rerank"
	"gexue/internal/retrieval"
)

// 端到端验证：双路召回（向量 + BM25）→ RRF 融合 → qwen3-rerank 精排。
// 运行：go run examples/search_demo.go
func main() {
	_ = godotenv.Load()
	_ = godotenv.Load("../../.env")

	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	dsn := cfg.DB.DSN
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	logger, _ := zap.NewDevelopment()

	emb := embedding.NewDashScope(cfg.Embedding.APIKey, cfg.Embedding.Model, cfg.Embedding.Dim)
	kbRepo := repo.NewKnowledgeRepo(db)

	var reranker rerank.Reranker
	if cfg.Rerank.Enabled && cfg.Rerank.APIKey != "" {
		reranker = rerank.NewDashScopeWithURL(cfg.Rerank.APIKey, cfg.Rerank.Model, cfg.Rerank.BaseURL)
		logger.Info("rerank enabled", zap.String("model", cfg.Rerank.Model))
	} else {
		logger.Warn("rerank disabled")
	}

	rt := retrieval.NewRetriever(kbRepo, emb, reranker, logger, retrieval.WithRerankTopN(cfg.Rerank.TopN))

	// 知识库 2（一年级语文）属于 user 2
	queries := []struct{ query string; topK int }{
		{"秋天的天气", 3},
		{"古诗 贾岛", 3},
		{"识字 拼音", 3},
	}
	ctx := context.Background()
	for _, q := range queries {
		fmt.Printf("\n===== query=%q topK=%d =====\n", q.query, q.topK)
		chunks, err := rt.Search(ctx, 2, 2, q.query, q.topK)
		if err != nil {
			fmt.Printf("search error: %v\n", err)
			continue
		}
		for i, c := range chunks {
			txt := c.ContentText
			if len(txt) > 60 {
				txt = txt[:60] + "..."
			}
			fmt.Printf("  #%d id=%d kb=%d score_note=%q\n", i+1, c.ID, c.KbID, txt)
		}
	}
}

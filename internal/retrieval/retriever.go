// Package retrieval 双路召回检索编排：向量召回 + BM25 关键词召回 → RRF 融合 → rerank 精排。
// 统一供 KnowledgeService.SearchInKb 与 Agent 检索使用。
package retrieval

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pgvector/pgvector-go"
	"go.uber.org/zap"

	"gexue/internal/embedding"
	"gexue/internal/metrics"
	"gexue/internal/model"
	"gexue/internal/repo"
	"gexue/internal/rerank"
)

// chunkSource BM25 索引所需的分块数据源（CountChunks 作缓存版本、ListChunksForSearch 全量拉取）。
// 生产实现为 *repo.KnowledgeRepo；测试可注入内存 fake 验证缓存重建。
type chunkSource interface {
	CountChunks(ctx context.Context, kbID uint) (int64, error)
	ListChunksForSearch(ctx context.Context, kbID uint) ([]model.KnowledgeChunk, error)
}

// Retriever 检索编排器。
// reranker 可为 nil（未配置 rerank key 时跳过精排，仅返回 RRF 融合结果）。
type Retriever struct {
	repo     *repo.KnowledgeRepo
	chunks   chunkSource // BM25 数据源，默认即 repo，测试可注入
	emb      embedding.Embedder
	reranker rerank.Reranker
	log      *zap.Logger
	metrics  *metrics.RetrievalMetrics // Prometheus 检索链路指标（nil 时跳过埋点）

	vectorTopK  int // 向量召回数量
	keywordTopK int // BM25 召回数量
	rerankTopN  int // 送精排的候选数（从 RRF 融合结果取前 N）
	rrfK        int // RRF 常数

	bm25Cache sync.Map // kbID(uint) → *bm25CacheEntry：BM25 倒排索引缓存
}

// bm25CacheEntry BM25 索引缓存项。
type bm25CacheEntry struct {
	version int64 // 知识库分块数（COUNT），作为内容版本：变化即重建
	idx     *bm25Index
}

// RetrieverOption 配置项（函数式选项，便于调用方按需覆盖）。
type RetrieverOption func(*Retriever)

// WithVectorTopK 设置向量召回数量。
func WithVectorTopK(n int) RetrieverOption { return func(r *Retriever) { r.vectorTopK = n } }

// WithKeywordTopK 设置 BM25 召回数量。
func WithKeywordTopK(n int) RetrieverOption { return func(r *Retriever) { r.keywordTopK = n } }

// WithRerankTopN 设置送精排的候选数。
func WithRerankTopN(n int) RetrieverOption { return func(r *Retriever) { r.rerankTopN = n } }

// WithRRFK 设置 RRF 融合常数。
func WithRRFK(k int) RetrieverOption { return func(r *Retriever) { r.rrfK = k } }

// WithMetrics 注入 Prometheus 检索链路指标（nil 时跳过埋点，不影响既有逻辑）。
func WithMetrics(m *metrics.RetrievalMetrics) RetrieverOption {
	return func(r *Retriever) { r.metrics = m }
}

// NewRetriever 构造检索编排器。
func NewRetriever(r *repo.KnowledgeRepo, e embedding.Embedder, reranker rerank.Reranker, log *zap.Logger, opts ...RetrieverOption) *Retriever {
	rt := &Retriever{
		repo: r, chunks: r, emb: e, reranker: reranker, log: log,
		vectorTopK: 20, keywordTopK: 20, rerankTopN: 20, rrfK: DefaultRRFK,
	}
	for _, o := range opts {
		o(rt)
	}
	return rt
}

// Search 双路召回 → RRF 融合 → rerank 精排，返回最终 topK 分块。
// 与旧版 SearchInKb 同语义：严格限定所选知识库（kb_id 隔离）。
// 埋点：defer 统一记录总耗时与请求结果；各阶段分别记录耗时与召回条数。
func (r *Retriever) Search(ctx context.Context, userID, kbID uint, query string, topK int) (chunks []model.KnowledgeChunk, err error) {
	start := time.Now()
	defer func() {
		result := "success"
		if err != nil {
			result = "error"
		}
		r.metrics.ObserveStage("total", time.Since(start))
		r.metrics.IncRequests(result)
	}()

	if topK <= 0 {
		topK = 5
	}
	if r.emb == nil {
		return nil, fmt.Errorf("embedder not configured")
	}
	if _, err := r.repo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}

	// ① 向量召回（HNSW + 余弦）
	vecTopK := r.vectorTopK
	if vecTopK < topK*3 {
		vecTopK = topK * 3
	}
	vecStart := time.Now()
	vecs, err := r.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	vecChunks, err := r.repo.SearchInKb(ctx, kbID, pgvector.NewVector(vecs[0]), vecTopK)
	if err != nil {
		return nil, fmt.Errorf("vector recall: %w", err)
	}
	r.metrics.ObserveStage("vector", time.Since(vecStart))
	r.metrics.ObserveRecall("vector", len(vecChunks))
	if r.log != nil {
		r.log.Debug("vector recall",
			zap.Uint("kb_id", kbID), zap.String("query", query),
			zap.Int("recalled", len(vecChunks)), zap.Int("topk", vecTopK))
	}

	// ② BM25 关键词召回（失败仅告警，降级为向量单路）
	kwTopK := r.keywordTopK
	if kwTopK < topK*3 {
		kwTopK = topK * 3
	}
	kwStart := time.Now()
	var kwChunks []model.KnowledgeChunk
	if idx, idxErr := r.getBM25Index(ctx, kbID); idxErr == nil {
		kwChunks = idx.Search(query, kwTopK)
	} else if r.log != nil {
		r.log.Warn("bm25 recall failed, fallback to vector-only",
			zap.Uint("kb_id", kbID), zap.Error(idxErr))
		r.metrics.IncFallback("bm25_failed")
	}
	r.metrics.ObserveStage("bm25", time.Since(kwStart))
	r.metrics.ObserveRecall("bm25", len(kwChunks))
	if r.log != nil {
		r.log.Debug("bm25 recall",
			zap.Uint("kb_id", kbID), zap.String("query", query),
			zap.Int("recalled", len(kwChunks)), zap.Int("topk", kwTopK))
	}

	// ③ RRF 融合
	rrfStart := time.Now()
	fused := RRF([][]model.KnowledgeChunk{vecChunks, kwChunks}, r.rrfK)
	r.metrics.ObserveStage("rrf", time.Since(rrfStart))

	// ④ rerank 精排（未配置或失败 → 回退 RRF 结果）
	if r.reranker != nil && len(fused) > 0 {
		rkStart := time.Now()
		if reranked, rkErr := r.rerank(ctx, query, fused, topK); rkErr == nil {
			r.metrics.ObserveStage("rerank", time.Since(rkStart))
			if r.log != nil {
				r.log.Debug("rerank done", zap.Uint("kb_id", kbID), zap.Int("candidates", len(fused)), zap.Int("returned", len(reranked)))
			}
			return reranked, nil
		} else if r.log != nil {
			r.log.Warn("rerank failed, fallback to rrf result", zap.Error(rkErr))
			r.metrics.IncFallback("rerank_failed")
		}
	}

	// ⑤ 无 rerank：截断 RRF 结果
	if len(fused) > topK {
		fused = fused[:topK]
	}
	return fused, nil
}

// getBM25Index 取 kbID 的 BM25 倒排索引：
//   - 缓存命中且版本（分块 COUNT）一致 → 直接复用；
//   - 缓存缺失或版本变化（新增/删除分块）→ 全量拉取分块重建并缓存。
//
// 每次查询一次 COUNT（走 kb_id 索引）+ 一次倒排查询，不再全量重建/全量打分。
func (r *Retriever) getBM25Index(ctx context.Context, kbID uint) (*bm25Index, error) {
	version, err := r.chunks.CountChunks(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if v, ok := r.bm25Cache.Load(kbID); ok {
		entry := v.(*bm25CacheEntry)
		if entry.version == version {
			r.metrics.IncBM25Cache(true)
			return entry.idx, nil
		}
	}
	r.metrics.IncBM25Cache(false)
	allChunks, err := r.chunks.ListChunksForSearch(ctx, kbID)
	if err != nil {
		return nil, err
	}
	idx := buildBM25Index(allChunks, defaultSegmenter())
	r.bm25Cache.Store(kbID, &bm25CacheEntry{version: version, idx: idx})
	return idx, nil
}

// rerank 取融合后前 rerankTopN 送精排，按相关性分重排后截断 topK。
// documents 传给 reranker 后其 Index 指向该切片，据此重排原 chunk。
func (r *Retriever) rerank(ctx context.Context, query string, fused []model.KnowledgeChunk, topK int) ([]model.KnowledgeChunk, error) {
	n := r.rerankTopN
	if n <= 0 || n > len(fused) {
		n = len(fused)
	}
	cands := fused[:n]
	docs := make([]string, len(cands))
	for i, c := range cands {
		docs[i] = c.ContentText
	}
	results, err := r.reranker.Rerank(ctx, query, docs, len(cands))
	if err != nil {
		return nil, err
	}
	// Index → 原 chunk；结果已按分数降序。未覆盖的候选按原顺序追加兜底。
	used := make([]bool, len(cands))
	out := make([]model.KnowledgeChunk, 0, len(results))
	for _, res := range results {
		if res.Index >= 0 && res.Index < len(cands) && !used[res.Index] {
			used[res.Index] = true
			out = append(out, cands[res.Index])
		}
	}
	for i, c := range cands {
		if !used[i] {
			out = append(out, c)
		}
	}
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

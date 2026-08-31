// Package rerank 文本重排抽象。
// 当前实现：DashScope qwen3-rerank（交叉编码精排），复用 DASHSCOPE_API_KEY。
package rerank

import "context"

// Reranker 交叉编码精排接口：给定查询与候选文档，返回按相关性降序的 topN。
type Reranker interface {
	// Rerank 对 documents 精排，返回结果按相关性降序，Index 指向 documents 下标。
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]Result, error)
}

// Result 单个精排结果。
type Result struct {
	Index int     // 指向输入 documents 的下标
	Score float64 // relevance_score，越大越相关
}

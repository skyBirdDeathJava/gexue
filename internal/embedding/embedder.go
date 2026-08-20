// Package embedding 文本向量化抽象。
// 当前实现：DashScope text-embedding-v3（OpenAI 兼容 /embeddings，512 维）。
// 接口便于未来切换 方舟 / 本地 bge-small-zh（同为 512 维，免迁移）。
package embedding

import "context"

// Embedder 文本向量化接口。
type Embedder interface {
	// Embed 批量向量化，返回与 texts 等长的向量列表。
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dim 向量维度。
	Dim() int
}

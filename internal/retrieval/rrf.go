package retrieval

import (
	"sort"

	"gexue/internal/model"
)

// DefaultRRFK RRF 融合常数 k（标准经验值 60）。
// score(d) = Σ 1/(k + rank_i(d))，rank 从 1 开始。
const DefaultRRFK = 60

// RRF 融合多路召回列表（Reciprocal Rank Fusion）。
// 以 chunk 稳定 key（ID；ID=0 时退化为 ContentText）去重，按融合分降序返回。
func RRF(lists [][]model.KnowledgeChunk, k int) []model.KnowledgeChunk {
	if k <= 0 {
		k = DefaultRRFK
	}
	scores := make(map[string]float64)
	byKey := make(map[string]model.KnowledgeChunk)
	for _, list := range lists {
		for rank, ch := range list {
			key := chunkKey(ch)
			scores[key] += 1.0 / float64(k+rank+1) // rank 从 1 开始
			byKey[key] = ch
		}
	}
	res := make([]model.KnowledgeChunk, 0, len(scores))
	for key := range scores {
		res = append(res, byKey[key])
	}
	// 分数降序；分数相同按 ID 升序（确定性 tiebreaker，避免 map 迭代随机性影响输出）。
	sort.SliceStable(res, func(i, j int) bool {
		si, sj := scores[chunkKey(res[i])], scores[chunkKey(res[j])]
		if si != sj {
			return si > sj
		}
		return res[i].ID < res[j].ID
	})
	return res
}

// chunkKey chunk 的稳定去重键：ID 非 0 用 ID，否则退化用文本（测试/未落库场景）。
func chunkKey(ch model.KnowledgeChunk) string {
	if ch.ID != 0 {
		return "id:" + uintToString(ch.ID)
	}
	return "text:" + ch.ContentText
}

// uintToString 避免引入 strconv 之外的依赖（保持小函数可读）。
func uintToString(u uint) string {
	if u == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}

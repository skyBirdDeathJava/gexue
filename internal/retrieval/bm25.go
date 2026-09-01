package retrieval

import (
	"math"
	"sort"
	"unicode"

	"gexue/internal/model"
)

// BM25 参数（经典默认值）。
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// bm25Index 可复用的 BM25 倒排索引（一次构建，多次 Search）。
// 倒排结构：term → 文档列表（含词频），另存文档长度与均值，供打分时直接取用，
// 避免每次查询对全库文档重新遍历打分。
type bm25Index struct {
	docs     []model.KnowledgeChunk // 构建时持有的文档引用（Search 结果按此返回）
	df       map[string]int         // 词 → 含该词的文档数
	postings map[string][]bm25Hit
	docLen   []int
	avgdl    float64
}

// bm25Hit 词在某文档中的出现。
type bm25Hit struct {
	doc int
	tf  int
}

// buildBM25Index 构建倒排索引。传入的 docs 会被索引持有引用（供 Search 返回结果）。
// 分词使用包级共享的 gse 分词器（通用词典 + 领域词表 + bigram 回退）。
func buildBM25Index(docs []model.KnowledgeChunk, seg *segmenter) *bm25Index {
	idx := &bm25Index{
		docs:     docs,
		df:       make(map[string]int),
		postings: make(map[string][]bm25Hit),
		docLen:   make([]int, len(docs)),
	}
	totalLen := 0
	for i, d := range docs {
		toks := seg.tokenize(d.ContentText)
		idx.docLen[i] = len(toks)
		totalLen += len(toks)
		freq := make(map[string]int)
		for _, t := range toks {
			freq[t]++
		}
		for t, tf := range freq {
			if _, ok := idx.postings[t]; !ok {
				idx.postings[t] = nil
			}
			idx.postings[t] = append(idx.postings[t], bm25Hit{doc: i, tf: tf})
			idx.df[t]++
		}
	}
	if len(docs) > 0 {
		idx.avgdl = float64(totalLen) / float64(len(docs))
	}
	return idx
}

// Search 对 query 打分取 topK。
// score(D,Q) = Σ_{t∈Q} IDF(t) * f(t,D)*(k1+1) / (f(t,D) + k1*(1-b+b*|D|/avgdl))
// IDF(t) = ln((N - n(t) + 0.5) / (n(t) + 0.5) + 1)
// 仅对倒排中命中的文档打分（而非全库遍历）；分数 > 0 的文档按降序返回。
func (idx *bm25Index) Search(query string, topK int) []model.KnowledgeChunk {
	if len(idx.docs) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}
	queryTokens := unique(defaultSegmenter().tokenize(query))
	if len(queryTokens) == 0 {
		return nil
	}

	n := len(idx.docs)
	scores := make(map[int]float64, len(idx.docs))
	for _, t := range queryTokens {
		df := idx.df[t]
		if df == 0 {
			continue
		}
		idf := math.Log((float64(n-df)+0.5)/(float64(df)+0.5) + 1)
		for _, hit := range idx.postings[t] {
			docLen := idx.docLen[hit.doc]
			denom := float64(hit.tf) + bm25K1*(1-bm25B+bm25B*float64(docLen)/idx.avgdl)
			scores[hit.doc] += idf * float64(hit.tf) * (bm25K1 + 1) / denom
		}
	}

	// 命中文档按分数降序；同分按 ID 升序（确定性，避免 map 遍历序导致 flaky）
	order := make([]int, 0, len(scores))
	for doc := range scores {
		order = append(order, doc)
	}
	sort.SliceStable(order, func(i, j int) bool {
		di, dj := order[i], order[j]
		if scores[di] != scores[dj] {
			return scores[di] > scores[dj]
		}
		return idx.docs[di].ID < idx.docs[dj].ID
	})

	res := make([]model.KnowledgeChunk, 0, topK)
	for _, doc := range order {
		if scores[doc] <= 0 {
			break
		}
		res = append(res, idx.docs[doc])
		if len(res) >= topK {
			break
		}
	}
	return res
}

// BM25Search 便捷包装：构建索引后单次查询。
// 语义与旧版一致（兼容现有测试/调用）；重负载路径请直接复用 buildBM25Index 的索引。
func BM25Search(docs []model.KnowledgeChunk, query string, topK int) []model.KnowledgeChunk {
	idx := buildBM25Index(docs, defaultSegmenter())
	return idx.Search(query, topK)
}

// tokenize 分词（兼容旧测试的包级入口）：委托给包级共享分词器。
func tokenize(text string) []string {
	return defaultSegmenter().tokenize(text)
}

// isCJK 判断是否中文（CJK 统一表意文字）。
// 注意：不能依赖 unicode.IsLetter——它对 CJK 也返回 true，必须显式区分。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

// unique 去重并保持首次出现顺序。
func unique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

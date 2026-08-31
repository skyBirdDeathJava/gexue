package retrieval

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gexue/internal/model"
)

// BM25 参数（经典默认值）。
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// BM25Search 应用层 BM25 关键词检索：对 docs 打分取 topK。
// score(D,Q) = Σ_{t∈Q} IDF(t) * f(t,D)*(k1+1) / (f(t,D) + k1*(1-b+b*|D|/avgdl))
// IDF(t) = ln((N - n(t) + 0.5) / (n(t) + 0.5) + 1)
//
// 纯函数、无状态：每次查询由调用方传入 kb 内全部分块。
// TODO(优化): 库变大后按 kbID 缓存倒排索引，避免每次全量重建。
func BM25Search(docs []model.KnowledgeChunk, query string, topK int) []model.KnowledgeChunk {
	if len(docs) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}
	idx := buildBM25Index(docs)
	queryTokens := unique(tokenize(query))
	if len(queryTokens) == 0 {
		return nil
	}

	n := len(docs)
	scores := make([]float64, n)
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

	order := make([]int, 0, n)
	for i := range docs {
		order = append(order, i)
	}
	sort.SliceStable(order, func(i, j int) bool { return scores[order[i]] > scores[order[j]] })

	res := make([]model.KnowledgeChunk, 0, topK)
	for _, i := range order {
		if scores[i] <= 0 {
			break
		}
		res = append(res, docs[i])
		if len(res) >= topK {
			break
		}
	}
	return res
}

// bm25Index BM25 倒排索引（单次查询用）。
type bm25Index struct {
	df       map[string]int // 词 → 含该词的文档数
	postings map[string][]bm25Hit
	docLen   []int
	avgdl    float64
}

// bm25Hit 词在某文档中的出现。
type bm25Hit struct {
	doc int
	tf  int
}

func buildBM25Index(docs []model.KnowledgeChunk) *bm25Index {
	idx := &bm25Index{
		df:       make(map[string]int),
		postings: make(map[string][]bm25Hit),
		docLen:   make([]int, len(docs)),
	}
	totalLen := 0
	for i, d := range docs {
		toks := tokenize(d.ContentText)
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

// tokenize 应用层中文分词：
//   - CJK 字符（含全角标点外的中文）：单字 + 相邻 bigram 切分；
//   - 连续 ASCII 字母/数字作为整词（按非字母数字边界切分）；
//   - 全角/半角标点与空白作为分隔符；
//   - 去掉长度 1 的 ASCII 字母/数字（弱信号、多为停用词）；
//   - 全部转小写（英文大小写归一）。
//
// 不依赖分词库/词典：中文按"单字 + bigram"覆盖多字词的首/尾字与二字词，
// 对知识库术语召回足够，零外部依赖。
func tokenize(text string) []string {
	var out []string
	runes := []rune(text)
	n := len(runes)
	for i := 0; i < n; {
		r := runes[i]
		switch {
		case isCJK(r):
			// 单字
			out = append(out, string(r))
			// 与右侧相邻的非分隔符构成 bigram（覆盖二字词与中英跨界对）
			if i+1 < n && !isSeparator(runes[i+1]) {
				out = append(out, string([]rune{r, runes[i+1]}))
			}
			i++
		case isWordRune(r):
			// 连续 ASCII 字母/数字整词（CJK 已在上面分支处理，不会混入）
			j := i
			for j < n && isWordRune(runes[j]) {
				j++
			}
			word := strings.ToLower(string(runes[i:j]))
			if utf8.RuneCountInString(word) > 1 { // 丢弃单字母/单数字
				out = append(out, word)
			}
			// 词尾与右侧相邻的非分隔符构成跨界 bigram（如 "x的"）；word 为 ASCII 词
			if j < n && !isSeparator(runes[j]) {
				out = append(out, word[len(word)-1:]+string(runes[j]))
			}
			i = j
		default:
			// 空白 / 标点：分隔符
			i++
		}
	}
	return out
}

// isCJK 判断是否中文（CJK 统一表意文字）。
// 注意：不能依赖 unicode.IsLetter——它对 CJK 也返回 true，必须显式区分。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

// isWordRune 判断是否构成英文/数字整词（字母或数字，但不含 CJK）。
func isWordRune(r rune) bool {
	return !isCJK(r) && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// isSeparator 判断是否分隔符（空白或标点，即既非 CJK 也非字母数字）。
func isSeparator(r rune) bool {
	return !isCJK(r) && !isWordRune(r)
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

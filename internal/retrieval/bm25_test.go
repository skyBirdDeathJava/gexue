package retrieval

import (
	"reflect"
	"testing"

	"gexue/internal/model"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"中文单字+bigram", "一元二次方程", []string{
			"一", "一元", "元", "元二", "二", "二次", "次", "次方", "方", "方程", "程",
		}},
		{"英文整词小写", "Apple PIE", []string{"apple", "pie"}},
		{"单字母/数字被丢弃", "a 1234 b 56", []string{"1234", "56"}},
		{"中文+英文混合", "解ax的方程", []string{
			"解", "解a", "ax", "x的", "的", "的方", "方", "方程", "程",
		}},
		{"标点作分隔符", "方程,解法；配方法", []string{
			"方", "方程", "程", "解", "解法", "法", "配", "配方", "方", "方法", "法",
		}},
		{"空串", "", nil},
		{"纯标点", "！！！，。", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenize(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenize(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBM25Search(t *testing.T) {
	t.Run("含关键词排前，无关文档不返回", func(t *testing.T) {
		docs := []model.KnowledgeChunk{
			chunk(1, "一元二次方程的解法与配方法"),
			chunk(2, "今天天气很好适合户外活动"),
			chunk(3, "一元二次方程判别式"),
		}
		got := BM25Search(docs, "一元二次方程", 5)
		if len(got) == 0 {
			t.Fatal("want results, got empty")
		}
		// 含关键词的两个文档应排最前（id 1、3），无关文档(id 2)分数为 0 被截断
		if got[0].ID == 2 || got[1].ID == 2 {
			t.Errorf("unrelated doc ranked: %v", ids(got))
		}
		if len(got) > 2 {
			t.Errorf("want at most 2 relevant, got %d: %v", len(got), ids(got))
		}
	})
	t.Run("词频高的排前", func(t *testing.T) {
		docs := []model.KnowledgeChunk{
			chunk(1, "方程方程方程方程的解法"),
			chunk(2, "方程 解法 配方法"),
		}
		got := BM25Search(docs, "方程", 2)
		if len(got) != 2 {
			t.Fatalf("want 2, got %d", len(got))
		}
		if got[0].ID != 1 {
			t.Errorf("high-freq doc should rank first, got %v", ids(got))
		}
	})
	t.Run("topK 截断", func(t *testing.T) {
		docs := []model.KnowledgeChunk{
			chunk(1, "方程 方程"), chunk(2, "方程 方程"), chunk(3, "方程 方程"), chunk(4, "方程 方程"),
		}
		got := BM25Search(docs, "方程", 2)
		if len(got) != 2 {
			t.Errorf("want 2, got %d", len(got))
		}
	})
	t.Run("空 docs", func(t *testing.T) {
		if got := BM25Search(nil, "方程", 5); got != nil {
			t.Errorf("want nil, got %v", ids(got))
		}
	})
	t.Run("空/无效 query", func(t *testing.T) {
		docs := []model.KnowledgeChunk{chunk(1, "一元二次方程")}
		if got := BM25Search(docs, "", 5); got != nil {
			t.Errorf("empty query want nil, got %v", ids(got))
		}
		if got := BM25Search(docs, "a", 5); got != nil { // 单字母被丢弃 → 无有效 token
			t.Errorf("no-token query want nil, got %v", ids(got))
		}
	})
	t.Run("英文检索", func(t *testing.T) {
		docs := []model.KnowledgeChunk{
			chunk(1, "the quick brown fox jumps over the lazy dog"),
			chunk(2, "完全无关的数学内容"),
		}
		got := BM25Search(docs, "quick fox", 2)
		if len(got) != 1 || got[0].ID != 1 {
			t.Errorf("english search wrong: %v", ids(got))
		}
	})
}

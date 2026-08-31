package retrieval

import (
	"context"
	"reflect"
	"testing"

	"github.com/go-ego/gse"

	"gexue/internal/model"
)

// TestTokenize 校准为 gse 实际输出。
// 注意：go test 的 cwd 是包目录，defaultSegmenter 找不到 configs/dict/edu_dict.txt，
// 故此处验证的是「内嵌通用词典」切分（领域词行为见 TestSegmenterDomainDict，另测）。
func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"词典多字词整词", "一元二次方程", []string{"一元二次方程"}},
		{"常见词整词", "方程", []string{"方程"}},
		{"英文整词小写", "Apple PIE", []string{"appl", "pie"}}, // snowball: apple→appl
		{"单字母/数字被丢弃", "a 1234 b 56", []string{"1234", "56"}},
		{"中英混合（未登录词回退）", "解ax的方程", []string{"解", "ax", "的", "方程"}},
		{"标点作分隔符", "方程,解法；配方法", []string{"方程", "解法", "配", "方法"}},
		{"未登录多字词 bigram 回退", "吕青柠", []string{"吕", "青", "柠", "吕青", "青柠"}},
		{"撇号缩略词整词", "don't", []string{"don't"}},
		{"撇号词形还原", "children's", []string{"children"}},
		{"英文词形还原", "running runs run", []string{"run", "run", "run"}},
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

// TestSegmenterDomainDict 领域词表行为（hermetic：不依赖磁盘文件，直接注入词表字符串）：
//   - 领域词整词切出（优先于 bigram 回退与拆词）；
//   - 内嵌通用词不受影响。
func TestSegmenterDomainDict(t *testing.T) {
	seg, err := gse.NewEmbed("zh")
	if err != nil {
		t.Fatalf("gse.NewEmbed: %v", err)
	}
	if err := loadDomainDict(&seg, []byte("配方法\n松下问童子\n秋天的天气\n")); err != nil {
		t.Fatalf("loadDomainDict: %v", err)
	}
	s := &segmenter{seg: seg}

	tests := []struct {
		in   string
		want []string
	}{
		{"配方法", []string{"配方法"}},
		{"松下问童子", []string{"松下问童子"}},
		{"秋天的天气", []string{"秋天的天气"}},
		{"一元二次方程的解法与配方法", []string{"一元二次方程", "的", "解法", "与", "配方法"}},
		// 内嵌词不受领域词影响
		{"方程", []string{"方程"}},
		{"一元二次方程", []string{"一元二次方程"}},
	}
	for _, tt := range tests {
		if got := s.tokenize(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tt.in, got, tt.want)
		}
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

// TestBM25IndexReuse 同一索引多次 Search 结果一致（构建与查询分离的可复用性）。
func TestBM25IndexReuse(t *testing.T) {
	docs := []model.KnowledgeChunk{
		chunk(1, "一元二次方程的解法与配方法"),
		chunk(2, "今天天气很好适合户外活动"),
	}
	idx := buildBM25Index(docs, defaultSegmenter())

	// 同一索引多次查询，结果稳定（无状态漂移）
	first := idx.Search("一元二次方程", 5)
	second := idx.Search("一元二次方程", 5)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("same index repeated Search differ: %v vs %v", ids(first), ids(second))
	}
	if len(first) == 0 || first[0].ID != 1 {
		t.Errorf("expect doc1 on top, got %v", ids(first))
	}
	// 不同查询互不影响
	if got := idx.Search("户外活动", 5); len(got) != 1 || got[0].ID != 2 {
		t.Errorf("independent query broken: %v", ids(got))
	}
}

// fakeChunkSource 内存版 chunkSource，用于验证 Retriever 级索引缓存重建。
type fakeChunkSource struct {
	chunks []model.KnowledgeChunk
}

func (f *fakeChunkSource) CountChunks(ctx context.Context, kbID uint) (int64, error) {
	return int64(len(f.chunks)), nil
}

func (f *fakeChunkSource) ListChunksForSearch(ctx context.Context, kbID uint) ([]model.KnowledgeChunk, error) {
	return f.chunks, nil
}

// TestBM25CacheRebuild 缓存复用与失效：
//   - 版本（分块数）未变 → 命中缓存，返回同一索引实例；
//   - 版本变化（新增分块）→ 重建索引，新分块可见。
func TestBM25CacheRebuild(t *testing.T) {
	fake := &fakeChunkSource{chunks: []model.KnowledgeChunk{chunk(1, "一元二次方程的解法与配方法")}}
	rt := &Retriever{chunks: fake}
	ctx := context.Background()
	const kbID = 1

	idx1, err := rt.getBM25Index(ctx, kbID)
	if err != nil {
		t.Fatalf("first getBM25Index: %v", err)
	}
	if len(idx1.Search("一元二次方程", 5)) != 1 {
		t.Fatal("index should find chunk 1")
	}

	// 版本未变 → 缓存命中（同一实例）
	idx2, err := rt.getBM25Index(ctx, kbID)
	if err != nil {
		t.Fatalf("cached getBM25Index: %v", err)
	}
	if idx2 != idx1 {
		t.Error("version unchanged: expect cache hit (same index instance)")
	}

	// 版本变化（新增分块）→ 重建
	fake.chunks = append(fake.chunks, chunk(2, "秋天的天气适合户外活动"))
	idx3, err := rt.getBM25Index(ctx, kbID)
	if err != nil {
		t.Fatalf("rebuilt getBM25Index: %v", err)
	}
	if idx3 == idx1 {
		t.Error("version changed: expect rebuild (new index instance)")
	}
	if len(idx3.docs) != 2 {
		t.Errorf("rebuilt index should hold 2 docs, got %d", len(idx3.docs))
	}
	if got := idx3.Search("户外活动", 5); len(got) != 1 || got[0].ID != 2 {
		t.Errorf("rebuilt index should see new chunk: %v", ids(got))
	}
}

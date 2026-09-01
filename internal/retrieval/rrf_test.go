package retrieval

import (
	"reflect"
	"testing"

	"gexue/internal/model"
)

func chunk(id uint, text string) model.KnowledgeChunk {
	return model.KnowledgeChunk{ID: id, ContentText: text}
}

func ids(chunks []model.KnowledgeChunk) []uint {
	out := make([]uint, len(chunks))
	for i, c := range chunks {
		out[i] = c.ID
	}
	return out
}

func TestRRF(t *testing.T) {
	t.Run("两路不重叠按分排序", func(t *testing.T) {
		a := []model.KnowledgeChunk{chunk(1, "a"), chunk(2, "b"), chunk(3, "c")}
		b := []model.KnowledgeChunk{chunk(4, "d"), chunk(5, "e")}
		got := RRF([][]model.KnowledgeChunk{a, b}, 60)
		if !reflect.DeepEqual(ids(got), []uint{1, 4, 2, 5, 3}) {
			t.Errorf("order = %v", ids(got))
		}
	})
	t.Run("两路重叠去重且分数累计", func(t *testing.T) {
		a := []model.KnowledgeChunk{chunk(1, "x"), chunk(2, "y")}
		b := []model.KnowledgeChunk{chunk(2, "y"), chunk(1, "x")}
		got := RRF([][]model.KnowledgeChunk{a, b}, 60)
		// 两者两路均出现 → 分相同，稳定排序保持原始次序
		if len(got) != 2 {
			t.Fatalf("want 2 unique, got %d", len(got))
		}
		if got[0].ID != 1 || got[1].ID != 2 {
			t.Errorf("ids = %v", ids(got))
		}
	})
	t.Run("一路为空", func(t *testing.T) {
		a := []model.KnowledgeChunk{chunk(7, "a"), chunk(8, "b")}
		got := RRF([][]model.KnowledgeChunk{a, nil}, 60)
		if !reflect.DeepEqual(ids(got), []uint{7, 8}) {
			t.Errorf("order = %v", ids(got))
		}
	})
	t.Run("全空", func(t *testing.T) {
		got := RRF([][]model.KnowledgeChunk{nil, nil}, 60)
		if len(got) != 0 {
			t.Errorf("want empty, got %v", ids(got))
		}
	})
	t.Run("k=0 用默认值", func(t *testing.T) {
		a := []model.KnowledgeChunk{chunk(1, "a"), chunk(2, "b")}
		b := []model.KnowledgeChunk{chunk(3, "c")}
		got := RRF([][]model.KnowledgeChunk{a, b}, 0)
		if len(got) != 3 {
			t.Errorf("want 3, got %d", len(got))
		}
	})
	t.Run("ID=0 退化按文本去重", func(t *testing.T) {
		a := []model.KnowledgeChunk{chunk(0, "same"), chunk(0, "other")}
		b := []model.KnowledgeChunk{chunk(0, "same")}
		got := RRF([][]model.KnowledgeChunk{a, b}, 60)
		if len(got) != 2 {
			t.Errorf("want 2 unique by text, got %d (%v)", len(got), ids(got))
		}
	})
}

func TestChunkKey(t *testing.T) {
	if chunkKey(chunk(42, "x")) != "id:42" {
		t.Errorf("id key = %q", chunkKey(chunk(42, "x")))
	}
	if chunkKey(chunk(0, "txt")) != "text:txt" {
		t.Errorf("text key = %q", chunkKey(chunk(0, "txt")))
	}
}

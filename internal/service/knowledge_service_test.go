package service

import (
	"testing"

	"gexue/internal/model"
)

func uintPtr(v uint) *uint { return &v }

// TestBuildTreeNested 三层树：根先出现在输入中，验证子节点仍能挂上（顺序无关）。
func TestBuildTreeNested(t *testing.T) {
	points := []model.KnowledgePoint{
		{ID: 1, Name: "数与运算"}, // 根
		{ID: 2, Name: "20以内加减法", ParentID: uintPtr(1)},
		{ID: 3, Name: "进位加法", ParentID: uintPtr(2)},
	}
	roots := buildTree(points)
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(roots))
	}
	if roots[0].ID != 1 {
		t.Fatalf("root id mismatch: %d", roots[0].ID)
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].ID != 2 {
		t.Fatalf("root children wrong: %+v", roots[0].Children)
	}
	child := roots[0].Children[0]
	if len(child.Children) != 1 || child.Children[0].ID != 3 {
		t.Fatalf("grandchild missing: %+v", child.Children)
	}
}

// TestBuildTreeOrphan 父缺失（孤儿）兜底为根。
func TestBuildTreeOrphan(t *testing.T) {
	points := []model.KnowledgePoint{
		{ID: 2, Name: "孤立节点", ParentID: uintPtr(99)},
	}
	roots := buildTree(points)
	if len(roots) != 1 || roots[0].ID != 2 {
		t.Fatalf("orphan should be root, got %+v", roots)
	}
}

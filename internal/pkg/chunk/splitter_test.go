package chunk

import (
	"strings"
	"testing"
)

// TestSplitShort 短内容不切分，整条返回。
func TestSplitShort(t *testing.T) {
	text := "20以内的加减法：3+5=8，7-2=5。"
	parts := Split(text, 500, 60)
	if len(parts) != 1 || parts[0] != text {
		t.Fatalf("short text should be single chunk, got %d parts: %v", len(parts), parts)
	}
}

// TestSplitLong 长文本按句子边界切分，块大小不超过 maxChunk。
func TestSplitLong(t *testing.T) {
	// 每句 24 字，30 句 = 720 字 > 500，应切多块
	text := strings.Repeat("小明有3个苹果，又买了5个，现在一共有8个苹果。", 30)
	parts := Split(text, 500, 60)
	if len(parts) < 2 {
		t.Fatalf("long text should split into multiple chunks, got %d", len(parts))
	}
	for i, p := range parts {
		if n := len([]rune(p)); n > 500 {
			t.Fatalf("chunk %d too large: %d runes", i, n)
		}
	}
}

// TestOverlap 相邻 chunk 间保留 overlap 尾部内容（衔接上下文）。
func TestOverlap(t *testing.T) {
	// 24 字 × 15 句 = 360 字 > 300，应切多块
	text := strings.Repeat("小明有3个苹果，又买了5个，现在一共有8个苹果。", 15)
	parts := Split(text, 300, 60)
	if len(parts) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(parts))
	}
	// 下一块开头应包含上一块尾部的 overlap 字符
	prevTail := []rune(parts[0])
	if len(prevTail) > 60 {
		prevTail = prevTail[len(prevTail)-60:]
	}
	head := []rune(parts[1])
	minLen := len(prevTail)
	if len(head) < minLen {
		minLen = len(head)
	}
	if minLen == 0 {
		t.Fatal("overlap empty")
	}
	if string(prevTail[:minLen]) != string(head[:minLen]) {
		t.Fatalf("overlap mismatch:\nprev tail=%q\nnext head=%q", prevTail, head)
	}
}

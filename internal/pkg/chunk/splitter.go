// Package chunk 文本分块：以句子边界优先切分，带 overlap，保证语义完整。
// 参数见 docs/KNOWLEDGE-BASE-SOLUTION.md §5：maxChunk 300-500 字，overlap 50-80 字（15-20%）。
package chunk

import (
	"strings"
)

// Split 按句子边界切分文本，带 overlap。
// 短内容（≤ maxChunk）整条返回；单句超长按 maxChunk 硬切并保 overlap。
// 注：maxChunk 和 overlap 单位为字符数（不是字节数）
func Split(text string, maxChunk, overlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxChunk <= 0 {
		maxChunk = 500
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChunk {
		overlap = maxChunk / 4
	}

	runes := []rune(text)
	if len(runes) <= maxChunk {
		return []string{text}
	}

	sentences := splitSentences(text)
	var chunks []string
	var cur []rune
	for _, s := range sentences {
		sr := []rune(s)
		// 当前块装不下新句子：先切出，再用尾部 overlap 衔接
		if len(cur) > 0 && len(cur)+len(sr) > maxChunk {
			chunks = append(chunks, string(cur))
			tail := cur
			if len(tail) > overlap {
				tail = tail[len(tail)-overlap:]
			}
			cur = append([]rune{}, tail...)
			cur = append(cur, sr...)
			continue
		}
		cur = append(cur, sr...)
		// 单句超长：按 maxChunk 硬切，每刀保留 overlap 衔接
		for len(cur) > maxChunk {
			chunks = append(chunks, string(cur[:maxChunk]))
			cur = append([]rune{}, cur[maxChunk-overlap:]...)
		}
	}
	if len(cur) > 0 {
		chunks = append(chunks, string(cur))
	}
	return chunks
}

// splitSentences 按句子边界（。！？换行；;）切分，保留标点。
func splitSentences(text string) []string {
	var out []string
	var buf []rune
	for _, r := range text {
		buf = append(buf, r)
		switch r {
		case '。', '！', '？', '\n', '；', ';':
			if s := strings.TrimSpace(string(buf)); s != "" {
				out = append(out, s)
			}
			buf = nil
		}
	}
	if s := strings.TrimSpace(string(buf)); s != "" {
		out = append(out, s)
	}
	return out
}

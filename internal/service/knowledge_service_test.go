package service

import (
	"testing"
)

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "多个连续换行",
			input:    "行1\n\n\n行2",
			expected: "行1\n行2",
		},
		{
			name:     "带空格的换行",
			input:    "行1\n   \n行2",
			expected: "行1\n行2",
		},
		{
			name:     "行尾空白",
			input:    "行1   \n行2  ",
			expected: "行1\n行2",
		},
		{
			name:     "复杂混合",
			input:    "行1\n\n  \n\n行2\n\n\n行3",
			expected: "行1\n行2\n行3",
		},
		{
			name:     "空白行",
			input:    "  行1  \n   \n  行2  ",
			expected: "行1\n行2",
		},
		{
			name:     "前后空白",
			input:    "  \n\n行1\n行2\n\n  ",
			expected: "行1\n行2",
		},
		{
			name:     "正常文本（无变化）",
			input:    "行1\n行2\n行3",
			expected: "行1\n行2\n行3",
		},
		{
			name:     "单行",
			input:    "单行文本",
			expected: "单行文本",
		},
		{
			name:     "空文本",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeText(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeText() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func BenchmarkNormalizeText(b *testing.B) {
	input := "行1\n\n\n行2\n  \n行3\n\n\n行4"
	for i := 0; i < b.N; i++ {
		normalizeText(input)
	}
}

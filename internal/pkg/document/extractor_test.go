package document

import (
	"context"
	"os"
	"testing"

	"gexue/internal/pkg/volcengine"
)

// TestExtractTextFromTxt 测试 txt 文本提取
func TestExtractTextFromTxt(t *testing.T) {
	testFile := "/tmp/test.txt"
	content := "这是一个测试文本\n第二行内容\n第三行内容"

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	text, err := ExtractText(context.Background(), testFile)
	if err != nil {
		t.Fatalf("failed to extract text: %v", err)
	}

	if text != content {
		t.Errorf("expected %q, got %q", content, text)
	}
}

// TestExtractTextWithOCRClient 测试带 OCR 客户端的提取
func TestExtractTextWithOCRClient(t *testing.T) {
	// 创建测试 txt 文件
	testFile := "/tmp/test_with_ocr.txt"
	content := "这是带 OCR 客户端的测试"

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	// 即使传入 OCR 客户端，txt 文件也应该直接提取，不使用 OCR
	ak := os.Getenv("VOLCENGINE_VISUAL_AK")
	sk := os.Getenv("VOLCENGINE_VISUAL_SK")
	var client *volcengine.OCRClient
	if ak != "" && sk != "" {
		client = volcengine.NewOCRClient(ak, sk)
	}

	text, err := ExtractTextWithOCRClient(context.Background(), testFile, client)
	if err != nil {
		t.Fatalf("failed to extract text: %v", err)
	}

	if text != content {
		t.Errorf("expected %q, got %q", content, text)
	}
}

// TestExtractImageWithoutOCRClient 测试没有 OCR 客户端的图片提取（应该返回错误）
func TestExtractImageWithoutOCRClient(t *testing.T) {
	// 创建测试图片文件
	testFile := "/tmp/test.png"
	if err := os.WriteFile(testFile, []byte{0x89, 0x50, 0x4E, 0x47}, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	// 不提供 OCR 客户端，应该返回错误
	_, err := ExtractTextWithOCRClient(context.Background(), testFile, nil)
	if err == nil {
		t.Fatal("expected error when OCR client is nil")
	}

	if err.Error() != "OCR 客户端未初始化，请检查火山引擎配置（VOLCENGINE_VISUAL_AK/SK）" {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestUnsupportedFileType 测试不支持的文件类型
func TestUnsupportedFileType(t *testing.T) {
	testFile := "/tmp/test.xyz"
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	_, err := ExtractText(context.Background(), testFile)
	if err == nil {
		t.Fatal("expected error for unsupported file type")
	}

	if err.Error() != "unsupported file type: .xyz" {
		t.Errorf("unexpected error: %v", err)
	}
}

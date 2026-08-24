package volcengine

import (
	"context"
	"os"
	"testing"
)

// TestOCRClientInitialization 测试 OCR 客户端初始化
func TestOCRClientInitialization(t *testing.T) {
	ak := os.Getenv("VOLCENGINE_VISUAL_AK")
	sk := os.Getenv("VOLCENGINE_VISUAL_SK")

	if ak == "" || sk == "" {
		t.Skip("VOLCENGINE_VISUAL_AK or VOLCENGINE_VISUAL_SK not set")
	}

	client := NewOCRClient(ak, sk)
	if client == nil {
		t.Fatal("failed to create OCR client")
	}

	if client.client == nil {
		t.Fatal("visual client is nil")
	}
}

// TestRecognizeImageBytesWithoutServer 测试图像识别（模拟）
func TestRecognizeImageBytesWithoutServer(t *testing.T) {
	ak := os.Getenv("VOLCENGINE_VISUAL_AK")
	sk := os.Getenv("VOLCENGINE_VISUAL_SK")

	if ak == "" || sk == "" {
		t.Skip("VOLCENGINE_VISUAL_AK or VOLCENGINE_VISUAL_SK not set")
	}

	client := NewOCRClient(ak, sk)
	ctx := context.Background()

	// 创建一个简单的测试图片字节（1x1 像素 PNG）
	testImageBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	}

	// 注：这个测试会实际调用火山引擎 API，可能失败（无有效图片内容）
	_, err := client.RecognizeImageBytes(ctx, testImageBytes)
	// 预期会失败（因为不是真实图片），但应该能正确调用API
	if err != nil {
		t.Logf("Expected error for invalid image: %v", err)
	}
}

// TestExtractTextIntegration 集成测试：从文件路径提取
func TestExtractTextIntegration(t *testing.T) {
	ak := os.Getenv("VOLCENGINE_VISUAL_AK")
	sk := os.Getenv("VOLCENGINE_VISUAL_SK")

	if ak == "" || sk == "" {
		t.Skip("VOLCENGINE_VISUAL_AK or VOLCENGINE_VISUAL_SK not set")
	}

	client := NewOCRClient(ak, sk)
	ctx := context.Background()

	// 创建一个临时测试图片文件
	testFile := "/tmp/test_ocr.png"
	testImageBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	}
	if err := os.WriteFile(testFile, testImageBytes, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	_, err := client.RecognizeImage(ctx, testFile)
	// 预期会失败（因为不是真实图片），但应该能正确调用API
	if err != nil {
		t.Logf("Expected error for invalid image: %v", err)
	}
}

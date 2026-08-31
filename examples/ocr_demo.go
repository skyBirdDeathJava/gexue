//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"gexue/internal/pkg/document"
	"gexue/internal/pkg/volcengine"
)

// 演示：火山引擎 OCR 集成的完整流程
func main() {
	fmt.Println("====== 火山引擎 OCR 集成 - 端到端演示 ======\n")

	// 1. 从环境变量读取凭证
	ak := os.Getenv("VOLCENGINE_VISUAL_AK")
	sk := os.Getenv("VOLCENGINE_VISUAL_SK")

	if ak == "" || sk == "" {
		log.Printf("⚠️  火山引擎凭证未配置，某些功能将被禁用\n")
		log.Printf("   请在 .env 中设置：\n")
		log.Printf("   - VOLCENGINE_VISUAL_AK\n")
		log.Printf("   - VOLCENGINE_VISUAL_SK\n")
		ak, sk = "test_ak", "test_sk"
	} else {
		log.Printf("✓ 已加载火山引擎凭证\n")
	}

	// 2. 创建 OCR 客户端
	client := volcengine.NewOCRClient(ak, sk)
	log.Printf("✓ OCR 客户端已初始化\n\n")

	// 3. 演示文本提取的多种方式
	ctx := context.Background()

	fmt.Println("====== 1. 演示：TXT 文件提取 ======")
	demonstrateTxtExtraction(ctx)

	fmt.Println("\n====== 2. 演示：带 OCR 的文本提取 ======")
	demonstrateOCRExtraction(ctx, client)

	fmt.Println("\n====== 3. 演示：错误处理 ======")
	demonstrateErrorHandling(ctx, client)

	fmt.Println("\n====== 完成！✓ ======\n")
	fmt.Println("相关文档：")
	fmt.Println("  - OCR_INTEGRATION.md       : 完整的集成文档")
	fmt.Println("  - demo_ocr.sh              : 自动化测试脚本")
	fmt.Println("  - internal/pkg/volcengine : OCR 客户端实现")
	fmt.Println()
}

func demonstrateTxtExtraction(ctx context.Context) {
	// 创建测试文件
	testFile := "/tmp/demo_test.txt"
	content := "这是一个测试文本文件\n第二行\n第三行"

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		log.Printf("✗ 创建测试文件失败: %v\n", err)
		return
	}
	defer os.Remove(testFile)

	// 方式 1：直接提取（不需要 OCR）
	log.Printf("方式 1：直接提取 TXT 文件\n")
	text, err := document.ExtractText(ctx, testFile)
	if err != nil {
		log.Printf("✗ 提取失败: %v\n", err)
		return
	}
	log.Printf("✓ 提取成功\n")
	log.Printf("  内容: %q\n", text)
}

func demonstrateOCRExtraction(ctx context.Context, client *volcengine.OCRClient) {
	// 创建测试文本文件
	testFile := "/tmp/demo_ocr_test.txt"
	content := "使用 OCR 客户端提取的内容"

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		log.Printf("✗ 创建测试文件失败: %v\n", err)
		return
	}
	defer os.Remove(testFile)

	// 方式 2：使用 ExtractTextWithOCRClient
	log.Printf("方式 2：使用 ExtractTextWithOCRClient\n")
	log.Printf("  即使指定了 OCR 客户端，TXT 文件也会直接提取（不使用 OCR）\n")
	text, err := document.ExtractTextWithOCRClient(ctx, testFile, client)
	if err != nil {
		log.Printf("✗ 提取失败: %v\n", err)
		return
	}
	log.Printf("✓ 提取成功\n")
	log.Printf("  内容: %q\n", text)

	// 演示图片提取（需要 OCR）
	log.Printf("\n方式 3：创建测试图片并尝试 OCR 识别\n")
	imageFile := "/tmp/demo_test.png"
	// 创建最小 PNG 文件头
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D,
	}
	if err := os.WriteFile(imageFile, pngHeader, 0644); err != nil {
		log.Printf("✗ 创建测试图片失败: %v\n", err)
		return
	}
	defer os.Remove(imageFile)

	log.Printf("  尝试识别 PNG 文件...\n")
	text2, err := document.ExtractTextWithOCRClient(ctx, imageFile, client)
	if err != nil {
		// 预期会失败（因为不是真实图片），但这是正常的
		if os.IsNotExist(err) {
			log.Printf("✗ 文件不存在\n")
		} else {
			log.Printf("⚠️  识别失败（预期的，因为这不是真实图片）\n")
			log.Printf("  错误: %v\n", err)
			log.Printf("  提示：使用真实的 JPG/PNG/PDF 文件可以成功识别\n")
		}
	} else {
		log.Printf("✓ 识别成功\n")
		log.Printf("  内容: %q\n", text2)
	}
}

func demonstrateErrorHandling(ctx context.Context, client *volcengine.OCRClient) {
	// 场景 1：不提供 OCR 客户端尝试识别图片
	log.Printf("场景 1：不提供 OCR 客户端识别图片\n")
	imageFile := "/tmp/demo_error_test.png"
	if err := os.WriteFile(imageFile, []byte{0x89, 0x50}, 0644); err != nil {
		log.Printf("✗ 创建测试文件失败: %v\n", err)
		return
	}
	defer os.Remove(imageFile)

	_, err := document.ExtractTextWithOCRClient(ctx, imageFile, nil)
	if err != nil {
		log.Printf("✓ 正确的错误处理\n")
		log.Printf("  错误: %v\n", err)
	}

	// 场景 2：不支持的文件格式
	log.Printf("\n场景 2：不支持的文件格式\n")
	unsupportedFile := "/tmp/demo_unsupported.xyz"
	if err := os.WriteFile(unsupportedFile, []byte("test"), 0644); err != nil {
		log.Printf("✗ 创建测试文件失败: %v\n", err)
		return
	}
	defer os.Remove(unsupportedFile)

	_, err = document.ExtractText(ctx, unsupportedFile)
	if err != nil {
		log.Printf("✓ 正确的错误处理\n")
		log.Printf("  错误: %v\n", err)
	}

	// 场景 3：文件不存在
	log.Printf("\n场景 3：文件不存在\n")
	_, err = document.ExtractText(ctx, "/tmp/nonexistent_file_12345.txt")
	if err != nil {
		log.Printf("✓ 正确的错误处理\n")
		log.Printf("  错误: %v\n", err)
	}
}

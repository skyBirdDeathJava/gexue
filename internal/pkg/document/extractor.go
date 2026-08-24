package document

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"

	"gexue/internal/pkg/volcengine"
)

// ExtractText 根据文件类型提取文本内容
func ExtractText(ctx context.Context, filePath string) (string, error) {
	return ExtractTextWithOCRClient(ctx, filePath, nil)
}

// ExtractTextWithOCRClient 根据文件类型提取文本内容，支持 OCR
func ExtractTextWithOCRClient(ctx context.Context, filePath string, ocrClient *volcengine.OCRClient) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".txt", ".md", ".markdown":
		return extractTxt(filePath)
	case ".docx":
		return extractDocx(filePath, ocrClient)
	case ".pdf":
		return extractPdf(ctx, filePath, ocrClient)
	case ".jpg", ".jpeg", ".png":
		return extractImageOCR(ctx, filePath, ocrClient)
	default:
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}
}

// extractTxt 提取 txt 文件内容
func extractTxt(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// extractDocx 提取 docx 文件内容
// DOCX 是 ZIP 格式，包含 word/document.xml 的 WordML 文本
func extractDocx(filePath string, ocrClient *volcengine.OCRClient) (string, error) {
	// 验证文件是否存在和可读
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to access docx file: %w", err)
	}
	if fileInfo.Size() == 0 {
		return "", fmt.Errorf("docx file is empty")
	}

	// 验证文件是否为有效的 ZIP 格式（DOCX 的前4字节应该是 ZIP 签名）
	data := make([]byte, 4)
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open docx file for validation: %w", err)
	}
	defer f.Close()

	if _, err := f.Read(data); err != nil {
		return "", fmt.Errorf("failed to read docx file header: %w", err)
	}

	// ZIP 文件应该以 PK 开头（0x50 0x4B）
	if data[0] != 0x50 || data[1] != 0x4B {
		return "", fmt.Errorf("invalid docx file format: not a valid ZIP file (missing ZIP signature)")
	}

	// 打开 ZIP 文件
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open docx file as ZIP: %w", err)
	}
	defer r.Close()

	// 查找 word/document.xml
	var docFile io.ReadCloser
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile, err = f.Open()
			if err != nil {
				return "", fmt.Errorf("failed to open document.xml: %w", err)
			}
			defer docFile.Close()
			break
		}
	}

	if docFile == nil {
		return "", fmt.Errorf("word/document.xml not found in docx")
	}

	// 解析 XML 并提取文本
	return extractTextFromWordML(docFile)
}

// WordML 文本元素
type textElement struct {
	Text string `xml:",chardata"`
}

type runElement struct {
	Texts []textElement `xml:"t"`
}

type paragraphElement struct {
	Runs []runElement `xml:"r"`
}

type bodyElement struct {
	Paragraphs []paragraphElement `xml:"p"`
}

type documentElement struct {
	Body bodyElement `xml:"body"`
}

// extractTextFromWordML 从 WordML XML 中提取文本（支持命名空间）
func extractTextFromWordML(reader io.Reader) (string, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read document.xml: %w", err)
	}

	// 移除命名空间前缀以简化解析
	text := string(data)
	text = strings.ReplaceAll(text, "w:", "")
	text = strings.ReplaceAll(text, "r:", "")
	text = strings.ReplaceAll(text, "v:", "")

	var doc documentElement
	err = xml.Unmarshal([]byte(text), &doc)
	if err != nil {
		return "", fmt.Errorf("failed to parse document.xml: %w", err)
	}

	var result strings.Builder
	for _, para := range doc.Body.Paragraphs {
		for _, run := range para.Runs {
			for _, t := range run.Texts {
				result.WriteString(t.Text)
			}
		}
		result.WriteString("\n")
	}

	return result.String(), nil
}

// extractPdf 提取 PDF 文件内容（支持多页）
func extractPdf(ctx context.Context, filePath string, ocrClient *volcengine.OCRClient) (string, error) {
	// 首先尝试使用 pdf 库提取文本（支持多页）
	text, err := extractPdfText(filePath)

	// 检查提取质量：如果有效内容足够，直接返回
	if err == nil && text != "" {
		// 计算有效字符比例
		validRatio := calculateValidCharRatio(text)
		if validRatio >= 0.7 {
			// 有效字符 >= 70%，可以接受
			return text, nil
		}
		// 否则乱码太多，需要 OCR 处理
	}

	// 如果 OCR 客户端不可用，返回现有的最好结果或错误
	if ocrClient == nil {
		if err != nil {
			return "", fmt.Errorf("pdf text extraction failed and ocr client not available: %w", err)
		}
		if text == "" {
			return "", fmt.Errorf("pdf text quality too poor and ocr client not available")
		}
		// 虽然质量不理想，但至少有一些文本
		return text, nil
	}

	// OCR 客户端可用，尝试更高质量的 OCR 处理
	// 但需要检查必要的工具是否安装
	ocrText, err := extractPdfWithOCRMultiPage(ctx, filePath, ocrClient)
	if err == nil && ocrText != "" {
		return ocrText, nil
	}

	// OCR 失败，回退到原始文本提取结果
	if err != nil {
		// 记录 OCR 失败，但如果原始提取有结果则返回
		if text != "" {
			return text, nil
		}
		// 两种方法都失败，返回 OCR 错误（更详细）
		return "", fmt.Errorf("pdf text extraction failed and ocr also failed: %w", err)
	}

	return "", fmt.Errorf("failed to extract pdf content")
}

// calculateValidCharRatio 计算文本中的有效字符比例
func calculateValidCharRatio(text string) float64 {
	if len(text) == 0 {
		return 0
	}

	validCount := 0
	for _, ch := range text {
		if isValidChar(ch) {
			validCount++
		}
	}

	return float64(validCount) / float64(len(text))
}

// isValidChar 判断字符是否有效
func isValidChar(ch rune) bool {
	// 中文汉字
	if ch >= 0x4E00 && ch <= 0x9FFF {
		return true
	}
	// CJK 标点
	if ch >= 0x3000 && ch <= 0x303F {
		return true
	}
	// 英文字母
	if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
		return true
	}
	// 数字
	if ch >= '0' && ch <= '9' {
		return true
	}
	// 空白（空格、制表符、换行符）
	if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
		return true
	}
	// ASCII 可打印字符（! 到 ~）
	if ch >= 0x0021 && ch <= 0x007E {
		return true
	}
	// 其他 Unicode 字母和标点
	if (ch >= 0x0100 && ch <= 0x017F) || // 拉丁补充 A
		(ch >= 0x0180 && ch <= 0x024F) { // 拉丁扩展 A/B
		return true
	}

	// 其他所有字符都算无效（包括 PDF 乱码）
	return false
}

// extractPdfWithOCRMultiPage 使用 OCR 逐页处理扫描版 PDF（支持多页）
// 使用 ImageMagick convert 命令将 PDF 转为图片，或 pdftoppm 作为替代方案
func extractPdfWithOCRMultiPage(ctx context.Context, filePath string, ocrClient *volcengine.OCRClient) (string, error) {
	// 临时目录存放转换的图片
	tmpDir := "./tmp/pdf-pages"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 尝试使用 ImageMagick convert，回退到 pdftoppm
	outputPattern := filepath.Join(tmpDir, "page.png")
	var cmd *exec.Cmd

	// 尝试 ImageMagick convert
	cmd = exec.CommandContext(ctx, "convert", "-density", "150", filePath, outputPattern)
	if err := cmd.Run(); err != nil {
		// 如果 convert 不可用，尝试 pdftoppm（Poppler工具）
		outputPatternPpm := filepath.Join(tmpDir, "page")
		cmd = exec.CommandContext(ctx, "pdftoppm", "-png", "-singlefile", filePath, outputPatternPpm)
		if err := cmd.Run(); err != nil {
			// 两个工具都不可用
			return "", fmt.Errorf(
				"failed to convert pdf to images: neither 'convert' nor 'pdftoppm' found. "+
					"Please install ImageMagick (brew install imagemagick) or Poppler (brew install poppler). Error: %w",
				err,
			)
		}
		// pdftoppm 成功，重命名文件为 page.png 格式
		ppmFile := filepath.Join(tmpDir, "page.png")
		if _, err := os.Stat(ppmFile); err == nil {
			// 文件已经是 page.png，无需重命名
		}
	}

	// 查找生成的图片
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", fmt.Errorf("failed to read temp dir: %w", err)
	}

	if len(files) == 0 {
		return "", fmt.Errorf("no pages converted from pdf")
	}

	var result strings.Builder

	// 逐页 OCR
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		pagePath := filepath.Join(tmpDir, file.Name())

		// OCR 识别该页面
		pageText, err := ocrClient.RecognizeImage(ctx, pagePath)
		if err != nil {
			continue // 跳过 OCR 失败的页面
		}

		if pageText != "" {
			if result.Len() > 0 {
				result.WriteString("\n")
			}
			result.WriteString(pageText)
		}
	}

	finalText := result.String()
	if finalText == "" {
		return "", fmt.Errorf("no text extracted from pdf via ocr")
	}

	return finalText, nil
}

// extractPdfText 使用 pdf 库提取 PDF 文本（支持多页）
func extractPdfText(filePath string) (string, error) {
	// 打开 PDF 文件
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open pdf: %w", err)
	}
	defer f.Close()

	// 获取页面数
	pageCount := r.NumPage()
	if pageCount == 0 {
		return "", fmt.Errorf("pdf has no pages")
	}

	var result strings.Builder

	// 遍历所有页面提取文本
	for pageNum := 1; pageNum <= pageCount; pageNum++ {
		// 获取页面
		p := r.Page(pageNum)

		// 提取该页面的文本
		text, err := p.GetPlainText(nil)
		if err != nil {
			// 跳过错误的页面，继续处理其他页面
			continue
		}

		// 清理提取的文本中的乱码
		// 移除控制字符和非打印字符
		cleanedText := cleanPDFText(text)

		if cleanedText != "" {
			result.WriteString(cleanedText)
			result.WriteString("\n")
		}
	}

	finalText := result.String()
	if finalText == "" {
		return "", fmt.Errorf("no text content extracted from pdf")
	}

	return finalText, nil
}

// cleanPDFText 清理 PDF 提取的文本中的乱码和控制字符
// 策略：只保留确定有效的字符，其他全部过滤
func cleanPDFText(text string) string {
	var sb strings.Builder

	for _, ch := range text {
		// 只保留以下字符：
		// 1. CJK 汉字 (0x4E00-0x9FFF)
		// 2. CJK 标点 (0x3000-0x303F)
		// 3. 英文字母 (A-Z, a-z)
		// 4. 数字 (0-9)
		// 5. ASCII 标点和符号 (0x0021-0x007E)
		// 6. 空白字符 (space, tab, newline, carriage return)
		// 7. 其他 Unicode 字母和标点 (但排除控制字符 < 0x0020)

		if (ch >= 0x4E00 && ch <= 0x9FFF) || // CJK 汉字
			(ch >= 0x3000 && ch <= 0x303F) || // CJK 标点
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') ||
			ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' ||
			(ch >= 0x0021 && ch <= 0x007E) || // ASCII 可打印
			(ch >= 0x0100 && ch <= 0x017F) || // 拉丁补充 A
			(ch >= 0x0180 && ch <= 0x024F) { // 拉丁扩展 A/B
			sb.WriteRune(ch)
		}
		// 其他所有字符（包括控制字符、奇怪的 Unicode）全部过滤
	}

	// 第二遍清理：合并多余空白
	result := sb.String()
	result = strings.TrimSpace(result)

	// 移除多个连续的换行
	re := regexp.MustCompile(`\n\n+`)
	result = re.ReplaceAllString(result, "\n")

	// 移除行尾空白
	lines := strings.Split(result, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	return strings.Join(cleanLines, "\n")
}

// countGarbage 计算文本中的乱码比例（不再使用，保留用于参考）
func countGarbage(text string) float64 {
	if len(text) == 0 {
		return 0
	}

	garageCount := 0
	validCount := 0

	for _, ch := range text {
		// 有效字符
		if (ch >= 0x4E00 && ch <= 0x9FFF) ||
			(ch >= 0x3000 && ch <= 0x303F) ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') ||
			ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' ||
			(ch >= 0x0021 && ch <= 0x007E) {
			validCount++
		} else {
			garageCount++
		}
	}

	total := garageCount + validCount
	if total == 0 {
		return 0
	}
	return float64(garageCount) / float64(total)
}

// extractImageOCR 通过火山引擎 OCR 提取图片/PDF 文本
func extractImageOCR(ctx context.Context, filePath string, ocrClient *volcengine.OCRClient) (string, error) {
	if ocrClient == nil {
		return "", fmt.Errorf("OCR 客户端未初始化，请检查火山引擎配置（VOLCENGINE_VISUAL_AK/SK）")
	}

	// 调用火山引擎 OCR
	text, err := ocrClient.RecognizeImage(ctx, filePath)
	if err != nil {
		return "", fmt.Errorf("OCR 识别失败: %w", err)
	}

	return text, nil
}

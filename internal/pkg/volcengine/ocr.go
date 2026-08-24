package volcengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/volcengine/volc-sdk-golang/service/visual"
)

// OCRClient 火山引擎 OCR 客户端
type OCRClient struct {
	ak string
	sk string
}

// NewOCRClient 创建 OCR 客户端
func NewOCRClient(ak, sk string) *OCRClient {
	return &OCRClient{
		ak: ak,
		sk: sk,
	}
}

// OCRNormalResponse OCR 响应（对应火山引擎 ocr_normal 方法）
type OCRNormalResponse struct {
	Code int           `json:"code"`
	Msg  string        `json:"msg"`
	Data OCRNormalData `json:"data"`
}

// OCRNormalData OCR 数据响应
type OCRNormalData struct {
	LineTexts []string `json:"line_texts"` // 识别到的文本行列表
}

// RecognizeImage 识别图片文本
func (c *OCRClient) RecognizeImage(ctx context.Context, imagePath string) (string, error) {
	// 读取图片文件
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("read image file: %w", err)
	}

	return c.RecognizeImageBytes(ctx, imageData)
}

// RecognizeImageBytes 识别图片字节
func (c *OCRClient) RecognizeImageBytes(ctx context.Context, imageBytes []byte) (string, error) {
	// 参数检查
	if c == nil {
		return "", fmt.Errorf("ocr client is nil")
	}
	if c.ak == "" || c.sk == "" {
		return "", fmt.Errorf("ocr credentials not configured")
	}
	if len(imageBytes) == 0 {
		return "", fmt.Errorf("image bytes is empty")
	}

	// 转换为 base64
	imageBase64 := base64.StdEncoding.EncodeToString(imageBytes)

	// 构造请求参数
	form := url.Values{}
	form.Add("image_base64", imageBase64)

	// 获取实例并设置凭证
	instance := visual.NewInstance()
	if instance == nil {
		return "", fmt.Errorf("failed to create visual instance")
	}
	if instance.Client == nil {
		return "", fmt.Errorf("visual instance client is nil")
	}

	instance.Client.SetAccessKey(c.ak)
	instance.Client.SetSecretKey(c.sk)

	// 调用 OCRNormal 方法（与 Python SDK 的 ocr_normal 对应）
	resp, status, err := instance.OCRNormal(form)
	if err != nil {
		return "", fmt.Errorf("ocr api call failed: status=%d, error=%w", status, err)
	}

	// 如果响应为 nil
	if resp == nil {
		return "", fmt.Errorf("ocr api returned nil response: status=%d", status)
	}

	// 解析响应
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}

	var ocrResp OCRNormalResponse
	if err := json.Unmarshal(respBytes, &ocrResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w, raw=%s", err, string(respBytes))
	}

	// 检查响应代码（火山引擎 ocr_normal 成功码是 10000）
	if ocrResp.Code != 10000 {
		return "", fmt.Errorf("ocr error: code=%d, msg=%s", ocrResp.Code, ocrResp.Msg)
	}

	// 提取文本（使用 line_texts 字段）
	var result string
	for _, line := range ocrResp.Data.LineTexts {
		result += line + "\n"
	}

	return result, nil
}

package oss

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// Config OSS 配置
type Config struct {
	AccessKeyID     string
	AccessKeySecret string
	Bucket          string
	Endpoint        string
	AccelEndpoint   string // 加速端点（可选）
}

// Uploader OSS 文件上传器
type Uploader struct {
	cfg    Config
	client *oss.Client
	bucket *oss.Bucket
}

// NewUploader 初始化 OSS 上传器
func NewUploader(cfg Config) (*Uploader, error) {
	// 创建 OSS 客户端
	client, err := oss.New(cfg.Endpoint, cfg.AccessKeyID, cfg.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create oss client: %w", err)
	}

	// 获取 Bucket 对象
	bucket, err := client.Bucket(cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket: %w", err)
	}

	return &Uploader{
		cfg:    cfg,
		client: client,
		bucket: bucket,
	}, nil
}

// Upload 上传文件到 OSS，返回文件 URL
// objectKey: OSS 中的对象键（路径），如 "knowledge-bases/kb1/file.docx"
// filePath: 本地文件路径
func (u *Uploader) Upload(objectKey, filePath string) (string, error) {
	// 打开本地文件
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// 上传到 OSS
	err = u.bucket.PutObject(objectKey, file)
	if err != nil {
		return "", fmt.Errorf("failed to upload to oss: %w", err)
	}

	// 构建 URL（使用标准端点，不使用加速端点用于生成访问 URL）
	// OSS URL 格式：https://bucket.endpoint/objectKey
	// 例如：https://interview-review-2026.oss-cn-beijing.aliyuncs.com/documents/1692547200_file.png

	// 使用标准端点生成 URL（而不是加速端点）
	// 因为加速端点的 URL 格式与标准端点不同，且可能无法直接访问
	endpoint := u.cfg.Endpoint

	// 移除 https:// 前缀以便组装
	if len(endpoint) > 8 && endpoint[:8] == "https://" {
		endpoint = endpoint[8:]
	}

	url := fmt.Sprintf("https://%s.%s/%s", u.cfg.Bucket, endpoint, objectKey)
	return url, nil
}

// UploadWithContent 直接上传内容（字节数据）
func (u *Uploader) UploadWithContent(objectKey string, content []byte) (string, error) {
	err := u.bucket.PutObject(objectKey, bytes.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("failed to upload to oss: %w", err)
	}

	// 构建 URL（使用标准端点）
	// OSS URL 格式：https://bucket.endpoint/objectKey
	// 例如：https://interview-review-2026.oss-cn-beijing.aliyuncs.com/documents/1692547200_file.png

	endpoint := u.cfg.Endpoint

	// 移除 https:// 前缀以便组装
	if len(endpoint) > 8 && endpoint[:8] == "https://" {
		endpoint = endpoint[8:]
	}

	url := fmt.Sprintf("https://%s.%s/%s", u.cfg.Bucket, endpoint, objectKey)
	return url, nil
}

// GenerateObjectKey 为原始文件生成 OSS 对象键
// 格式：documents/{timestamp}_{fileName}
// 例如：documents/1692547200_数学笔记.docx
func GenerateObjectKey(kbID uint, fileName string) string {
	timestamp := time.Now().Unix()
	return fmt.Sprintf("documents/%d_%s", timestamp, fileName)
}

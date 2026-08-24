package sms

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Provider SMS 服务提供者接口
type Provider interface {
	SendCode(phone string) error
	VerifyCode(phone, code string) bool
}

// LocalCache 本地验证码缓存
type LocalCache struct {
	codes map[string]VerifyCode
	mu    sync.RWMutex
}

type VerifyCode struct {
	Code      string
	ExpiresAt time.Time
}

// AliyunProvider 阿里云号码认证服务，通过短信 API 发送验证码
// 使用直接 HTTP API 调用，避免 SDK 的内网代理问题
type AliyunProvider struct {
	accessKeyID     string
	accessKeySecret string
	signName        string
	templateCode    string
	cache           *LocalCache
	httpClient      *http.Client
}

// NewAliyunProvider 初始化阿里云提供者
// 使用直接 HTTP API 调用而不是 SDK（避免内网代理问题）
func NewAliyunProvider(accessKeyID, accessKeySecret, signName, templateCode string) (*AliyunProvider, error) {
	return &AliyunProvider{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		signName:        signName,
		templateCode:    templateCode,
		cache: &LocalCache{
			codes: make(map[string]VerifyCode),
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

// aliyunSign 生成阿里云 API 签名
func (p *AliyunProvider) aliyunSign(params map[string]string) string {
	// 按键排序
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 构建字符串
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString("&")
		}
		sb.WriteString(url.QueryEscape(k))
		sb.WriteString("=")
		sb.WriteString(url.QueryEscape(params[k]))
	}
	stringToSign := "POST&%2F&" + url.QueryEscape(sb.String())

	// HMAC-SHA1 签名
	h := hmac.New(sha1.New, []byte(p.accessKeySecret+"&"))
	h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// SendCode 发送验证码到指定手机号
func (p *AliyunProvider) SendCode(phone string) error {
	code := fmt.Sprintf("%06d", rand.Intn(1000000))
	p.storeCodeLocally(phone, code)

	// 构建请求参数
	params := map[string]string{
		"AccessKeyId":      p.accessKeyID,
		"Action":           "SendSms",
		"Format":           "JSON",
		"PhoneNumbers":     phone,
		"RegionId":         "cn-beijing",
		"SignName":         p.signName,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   fmt.Sprintf("%d", time.Now().UnixNano()),
		"SignatureVersion": "1.0",
		"TemplateCode":     p.templateCode,
		"TemplateParam":    fmt.Sprintf(`{"code":"%s"}`, code),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          "2017-05-25",
	}

	// 计算签名
	params["Signature"] = p.aliyunSign(params)

	// 构建完整 URL
	baseURL := "https://dysmsapi.aliyuncs.com"
	query := url.Values{}
	for k, v := range params {
		query.Add(k, v)
	}

	// 发送 POST 请求
	resp, err := p.httpClient.Post(baseURL+"?"+query.Encode(), "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to send sms: %w", err)
	}
	defer resp.Body.Close()

	// 解析响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("aliyun api error: status=%d, body=%s", resp.StatusCode, string(body))
	}

	// 简单检查是否成功（实际应该解析 JSON）
	if strings.Contains(string(body), "\"Code\":\"OK\"") {
		fmt.Printf("[阿里云短信] 手机号 %s 验证码已发送\n", phone)
		return nil
	}

	return fmt.Errorf("aliyun sms api error: %s", string(body))
}

// VerifyCode 校验验证码
func (p *AliyunProvider) VerifyCode(phone, code string) bool {
	p.cache.mu.RLock()
	vc, exists := p.cache.codes[phone]
	p.cache.mu.RUnlock()

	if !exists {
		fmt.Printf("[SMS校验] 手机号 %s 未找到验证码\n", phone)
		return false
	}

	if time.Now().After(vc.ExpiresAt) {
		p.cache.mu.Lock()
		delete(p.cache.codes, phone)
		p.cache.mu.Unlock()
		fmt.Printf("[SMS校验] 手机号 %s 验证码已过期\n", phone)
		return false
	}

	if vc.Code != code {
		fmt.Printf("[SMS校验] 手机号 %s 验证码错误: 输入=%s, 正确=%s\n", phone, code, vc.Code)
		return false
	}

	p.cache.mu.Lock()
	delete(p.cache.codes, phone)
	p.cache.mu.Unlock()
	fmt.Printf("[SMS校验] 手机号 %s 验证码正确\n", phone)
	return true
}

func (p *AliyunProvider) storeCodeLocally(phone, code string) {
	p.cache.mu.Lock()
	p.cache.codes[phone] = VerifyCode{
		Code:      code,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	p.cache.mu.Unlock()
}

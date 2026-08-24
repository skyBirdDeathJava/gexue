package sms

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// LocalProvider 本地开发模式（内存存储）
type LocalProvider struct {
	codes map[string]VerifyCode
	mu    sync.RWMutex
}

func NewLocalProvider() *LocalProvider {
	return &LocalProvider{
		codes: make(map[string]VerifyCode),
	}
}

func (p *LocalProvider) SendCode(phone string) error {
	code := fmt.Sprintf("%06d", rand.Intn(1000000))
	p.mu.Lock()
	p.codes[phone] = VerifyCode{
		Code:      code,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	p.mu.Unlock()
	fmt.Printf("[SMS开发模式] 手机号 %s 验证码: %s\n", phone, code)
	return nil
}

func (p *LocalProvider) VerifyCode(phone, code string) bool {
	p.mu.RLock()
	vc, exists := p.codes[phone]
	p.mu.RUnlock()

	if !exists {
		fmt.Printf("[SMS校验] 手机号 %s 未找到验证码\n", phone)
		return false
	}

	if time.Now().After(vc.ExpiresAt) {
		p.mu.Lock()
		delete(p.codes, phone)
		p.mu.Unlock()
		fmt.Printf("[SMS校验] 手机号 %s 验证码已过期\n", phone)
		return false
	}

	if vc.Code != code {
		fmt.Printf("[SMS校验] 手机号 %s 验证码错误: 输入=%s, 正确=%s\n", phone, code, vc.Code)
		return false
	}

	p.mu.Lock()
	delete(p.codes, phone)
	p.mu.Unlock()
	fmt.Printf("[SMS校验] 手机号 %s 验证码正确\n", phone)
	return true
}

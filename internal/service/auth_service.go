// Package service 业务逻辑层。
package service

import (
	"context"
	"errors"
	"time"

	"gexue/internal/model"
	"gexue/internal/pkg/jwtutil"
	"gexue/internal/pkg/sms"
	"gexue/internal/repo"
)

var ErrInvalidCode = errors.New("invalid or expired verify code")

type AuthService struct {
	users       *repo.UserRepo
	secret      string
	ttl         time.Duration
	smsProvider sms.Provider
}

func NewAuthService(users *repo.UserRepo, jwtSecret string, ttl time.Duration, smsProvider sms.Provider) *AuthService {
	return &AuthService{users: users, secret: jwtSecret, ttl: ttl, smsProvider: smsProvider}
}

// SendVerifyCode 发送登录验证码。
func (s *AuthService) SendVerifyCode(ctx context.Context, phone string) error {
	if s.smsProvider == nil {
		return errors.New("sms provider not configured")
	}
	return s.smsProvider.SendCode(phone)
}

// LoginByPhone 手机号 + 验证码登录；未注册则自动创建账号。
func (s *AuthService) LoginByPhone(ctx context.Context, phone, code string) (token string, user *model.User, isNew bool, err error) {
	if s.smsProvider == nil {
		return "", nil, false, errors.New("sms provider not configured")
	}
	if !s.smsProvider.VerifyCode(phone, code) {
		return "", nil, false, ErrInvalidCode
	}

	u, err := s.users.GetByPhone(ctx, phone)
	if err != nil {
		if !errors.Is(err, repo.ErrNotFound) {
			return "", nil, false, err
		}
		u = &model.User{Phone: phone}
		if err := s.users.Create(ctx, u); err != nil {
			return "", nil, false, err
		}
		isNew = true
	}

	token, err = jwtutil.Sign(s.secret, u.ID, s.ttl)
	if err != nil {
		return "", nil, false, err
	}
	return token, u, isNew, nil
}

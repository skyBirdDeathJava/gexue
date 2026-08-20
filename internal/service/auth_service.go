// Package service 业务逻辑层。
package service

import (
	"context"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"

	"gexue/internal/model"
	"gexue/internal/pkg/jwtutil"
	"gexue/internal/repo"
)

var (
	ErrPhoneExists = errors.New("phone already registered")
	ErrBadLogin    = errors.New("phone or password incorrect")
)

type AuthService struct {
	users  *repo.UserRepo
	secret string
	ttl    time.Duration
}

func NewAuthService(users *repo.UserRepo, jwtSecret string, ttl time.Duration) *AuthService {
	return &AuthService{users: users, secret: jwtSecret, ttl: ttl}
}

// Register 手机号+密码注册（bcrypt 哈希，参考 mianba auth.py 的注册流程）。
func (s *AuthService) Register(ctx context.Context, phone, password string) (*model.User, error) {
	if _, err := s.users.GetByPhone(ctx, phone); err == nil {
		return nil, ErrPhoneExists
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &model.User{Phone: phone, PasswordHash: string(hash)}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// Login 校验并签发 JWT（HS256，ttl 默认 7 天，对齐 mianba ACCESS_TOKEN_EXPIRE_MINUTES）。
func (s *AuthService) Login(ctx context.Context, phone, password string) (string, *model.User, error) {
	u, err := s.users.GetByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return "", nil, ErrBadLogin
		}
		return "", nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return "", nil, ErrBadLogin
	}
	token, err := jwtutil.Sign(s.secret, u.ID, s.ttl)
	if err != nil {
		return "", nil, err
	}
	return token, u, nil
}

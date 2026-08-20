// Package repo 数据访问层。
package repo

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"gexue/internal/model"
)

var ErrNotFound = errors.New("not found")

type UserRepo struct{ db *gorm.DB }

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, u *model.User) error {
	return r.db.WithContext(ctx).Create(u).Error
}

func (r *UserRepo) GetByPhone(ctx context.Context, phone string) (*model.User, error) {
	var u model.User
	if err := r.db.WithContext(ctx).Where("phone = ?", phone).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) GetByID(ctx context.Context, id uint) (*model.User, error) {
	var u model.User
	if err := r.db.WithContext(ctx).First(&u, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

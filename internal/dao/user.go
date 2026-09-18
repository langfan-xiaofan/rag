package dao

import (
	"context"
	"rag/internal/model"

	"gorm.io/gorm"
)

type UserDao struct {
	db *gorm.DB
}

func NewUserDao(db *gorm.DB) *UserDao {
	return &UserDao{
		db: db,
	}
}

func (d *UserDao) CreateUser(ctx context.Context, user model.User) error {
	// Create 需要指针：创建时 GORM 会回填 ID / CreatedAt / UpdatedAt
	return d.db.WithContext(ctx).Create(&user).Error
}

func (d *UserDao) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	err := d.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *UserDao) GetUserByID(ctx context.Context, id uint) (*model.User, error) {
	var user model.User
	err := d.db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

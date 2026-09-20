package service

import (
	"context"
	"errors"
	"fmt"
	"rag/internal/dao"
	"rag/internal/dto"
	"rag/internal/model"
	"rag/utils/password"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type UserService struct {
	dao *dao.UserDao
}

func NewUserService(db *gorm.DB) *UserService {
	return &UserService{
		dao: dao.NewUserDao(db),
	}
}

func (svc *UserService) Register(ctx context.Context, req dto.RegisterReq) error {
	hashpassword, err := password.HashPassword(req.Password)
	if err != nil {
		return err
	}
	return svc.dao.CreateUser(ctx, model.User{
		Username: req.Username,
		Password: hashpassword,
	})
}

func (svc *UserService) Login(ctx context.Context, req dto.LoginReq) (uint, error) {
	var user *model.User
	user, err := svc.dao.GetUserByUsername(ctx, req.Username)
	if err != nil {
		return 0, err
	}
	hashpassworrd, err := password.HashPassword(req.Password)
	if err != nil {
		return 0, err
	}
	fmt.Printf("%s\n%s\n", hashpassworrd, user.Password)
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return 0, errors.New("账号或密码错误")
	}
	return user.ID, nil
}

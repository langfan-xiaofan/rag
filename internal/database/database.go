package database

import (
	"fmt"
	"rag/internal/config"
	"rag/internal/model"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Init 连接 MySQL 并完成表结构迁移。
// 连接参数来自 config.yaml 的 database.*（可被 DATABASE_* 环境变量覆盖）。
func Init() (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		config.Conf.Mysql.User,
		config.Conf.Mysql.Password,
		config.Conf.Mysql.Host,
		config.Conf.Mysql.Port,
		config.Conf.Mysql.Name,
	)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("连接 MySQL 失败，请确认数据库已启动且 config.yaml 的 database 配置正确: %w", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Session{}, &model.Message{}, &model.Summary{}); err != nil {
		return nil, fmt.Errorf("自动迁移表结构失败: %w", err)
	}
	return db, nil
}

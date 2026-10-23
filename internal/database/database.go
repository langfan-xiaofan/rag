package database

import (
	"context"
	"fmt"
	"log"
	"rag/internal/config"
	"rag/internal/model"
	"strings"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
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

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		return nil, fmt.Errorf("连接 MySQL 失败，请确认数据库已启动且 config.yaml 的 database 配置正确: %w", err)
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.KnowledgeBase{},
		&model.Session{},
		&model.Message{},
		&model.Summary{},
	); err != nil {
		return nil, fmt.Errorf("自动迁移表结构失败: %w", err)
	}
	return db, nil
}

// MigrateLegacyKnowledgeBases 把升级前“用户名即集合名”的数据挂到一个默认知识库下。
// 只处理还没有任何知识库、且确实存在旧集合或旧会话的用户；重复执行是安全的。
func MigrateLegacyKnowledgeBases(db *gorm.DB, qdrantClient *qdrant.Client) error {
	if db == nil || qdrantClient == nil {
		return nil
	}
	ctx := context.Background()
	users := make([]model.User, 0)
	if err := db.WithContext(ctx).Find(&users).Error; err != nil {
		return fmt.Errorf("读取用户列表失败: %w", err)
	}
	for _, user := range users {
		var kbCount int64
		if err := db.WithContext(ctx).Model(&model.KnowledgeBase{}).
			Where("user_id = ?", user.ID).Count(&kbCount).Error; err != nil {
			return fmt.Errorf("统计用户 %s 的知识库失败: %w", user.Username, err)
		}
		if kbCount > 0 {
			continue
		}
		var legacySessions int64
		if err := db.WithContext(ctx).Model(&model.Session{}).
			Where("user_id = ? AND knowledge_base_id = 0", user.ID).Count(&legacySessions).Error; err != nil {
			return fmt.Errorf("统计用户 %s 的旧会话失败: %w", user.Username, err)
		}
		docExists, err := qdrantClient.CollectionExists(ctx, user.Username)
		if err != nil {
			return fmt.Errorf("检查用户 %s 的旧文档集合失败: %w", user.Username, err)
		}
		fileExists, err := qdrantClient.CollectionExists(ctx, user.Username+"file")
		if err != nil {
			return fmt.Errorf("检查用户 %s 的旧文件集合失败: %w", user.Username, err)
		}
		if !docExists && !fileExists && legacySessions == 0 {
			continue
		}
		storageID := strings.ReplaceAll(uuid.NewString(), "-", "")
		kb := model.KnowledgeBase{
			UserID:         user.ID,
			Name:           "默认知识库",
			Description:    "升级前上传的历史文件",
			DocCollection:  user.Username,
			FileCollection: user.Username + "file",
			ObjectPrefix:   "kb_" + storageID,
		}
		if err := db.WithContext(ctx).Create(&kb).Error; err != nil {
			return fmt.Errorf("为用户 %s 创建默认知识库失败: %w", user.Username, err)
		}
		if err := db.WithContext(ctx).Model(&model.Session{}).
			Where("user_id = ? AND knowledge_base_id = 0", user.ID).
			Update("knowledge_base_id", kb.ID).Error; err != nil {
			return fmt.Errorf("回填用户 %s 的旧会话失败: %w", user.Username, err)
		}
		log.Printf("已为用户 %s 创建默认知识库（集合 %s / %s），旧会话 %d 条",
			user.Username, kb.DocCollection, kb.FileCollection, legacySessions)
	}
	return nil
}

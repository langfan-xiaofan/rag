package dao

import (
	"context"
	"rag/internal/model"

	"gorm.io/gorm"
)

type KnowledgeBaseDao struct {
	db *gorm.DB
}

func NewKnowledgeBaseDao(db *gorm.DB) *KnowledgeBaseDao {
	return &KnowledgeBaseDao{db: db}
}

func (d *KnowledgeBaseDao) Create(ctx context.Context, kb *model.KnowledgeBase) error {
	return d.db.WithContext(ctx).Create(kb).Error
}

func (d *KnowledgeBaseDao) ListByUser(ctx context.Context, userID uint) ([]model.KnowledgeBase, error) {
	rows := make([]model.KnowledgeBase, 0)
	err := d.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at ASC").
		Find(&rows).Error
	return rows, err
}

func (d *KnowledgeBaseDao) GetByIDAndUser(ctx context.Context, id, userID uint) (*model.KnowledgeBase, error) {
	var kb model.KnowledgeBase
	err := d.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&kb).Error
	if err != nil {
		return nil, err
	}
	return &kb, nil
}

func (d *KnowledgeBaseDao) GetByUserAndName(ctx context.Context, userID uint, name string) (*model.KnowledgeBase, error) {
	var kb model.KnowledgeBase
	err := d.db.WithContext(ctx).
		Where("user_id = ? AND name = ?", userID, name).
		First(&kb).Error
	if err != nil {
		return nil, err
	}
	return &kb, nil
}

func (d *KnowledgeBaseDao) Update(ctx context.Context, id, userID uint, name, description string) error {
	res := d.db.WithContext(ctx).
		Model(&model.KnowledgeBase{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{"name": name, "description": description})
	return res.Error
}

// DeleteWithSessions 在同一个事务里删掉知识库及其关联的会话、消息和摘要。
// 消息和摘要没有外键，按 session_id 显式清理，避免留下孤儿数据。
func (d *KnowledgeBaseDao) DeleteWithSessions(ctx context.Context, id, userID uint) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sessionIDs := make([]string, 0)
		if err := tx.Model(&model.Session{}).
			Where("user_id = ? AND knowledge_base_id = ?", userID, id).
			Pluck("id", &sessionIDs).Error; err != nil {
			return err
		}
		if len(sessionIDs) > 0 {
			if err := tx.Where("user_id = ? AND session_id IN ?", userID, sessionIDs).
				Delete(&model.Message{}).Error; err != nil {
				return err
			}
			if err := tx.Where("user_id = ? AND session_id IN ?", userID, sessionIDs).
				Delete(&model.Summary{}).Error; err != nil {
				return err
			}
			if err := tx.Where("user_id = ? AND knowledge_base_id = ?", userID, id).
				Delete(&model.Session{}).Error; err != nil {
				return err
			}
		}
		res := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&model.KnowledgeBase{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

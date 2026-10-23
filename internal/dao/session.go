package dao

import (
	"context"
	"time"

	"rag/internal/model"

	"gorm.io/gorm"
)

type SessionDao struct {
	db *gorm.DB
}

func NewSessionDao(db *gorm.DB) *SessionDao {
	return &SessionDao{db: db}
}

// SessionListItem 是会话列表的查询结果，带上知识库名称，避免前端再查一次。
type SessionListItem struct {
	ID                string
	Title             string
	KnowledgeBaseID   uint
	KnowledgeBaseName string
	CreatedAt         time.Time
}

func (d *SessionDao) ListByUser(ctx context.Context, userID uint) ([]SessionListItem, error) {
	rows := make([]SessionListItem, 0)
	err := d.db.WithContext(ctx).
		Table("sessions AS s").
		Select("s.id, s.title, s.knowledge_base_id, COALESCE(kb.name, '') AS knowledge_base_name, s.created_at").
		Joins("LEFT JOIN knowledge_bases AS kb ON kb.id = s.knowledge_base_id").
		Where("s.user_id = ? AND s.status = ?", userID, true).
		Order("s.created_at DESC").
		Scan(&rows).Error
	return rows, err
}

func (d *SessionDao) GetByIDAndUser(ctx context.Context, id string, userID uint) (*model.Session, error) {
	var session model.Session
	err := d.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND status = ?", id, userID, true).
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// GetMessages 返回会话的全部消息，包含工具调用和工具结果，由上层过滤成前端展示格式。
func (d *SessionDao) GetMessages(ctx context.Context, userID uint, sessionID string) ([]model.Message, error) {
	rows := make([]model.Message, 0)
	err := d.db.WithContext(ctx).
		Where("user_id = ? AND session_id = ?", userID, sessionID).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// SoftDelete 只把会话标记为不可用，消息保留在数据库里，列表和接口都不再返回。
func (d *SessionDao) SoftDelete(ctx context.Context, id string, userID uint) error {
	return d.db.WithContext(ctx).
		Model(&model.Session{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", false).Error
}

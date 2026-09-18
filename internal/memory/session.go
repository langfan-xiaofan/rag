package memory

import (
	"context"
	"errors"
	"fmt"
	"rag/internal/model"

	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

// 该模块主要用于长期记忆的设计。

type Session interface {
	CreateSession(session model.Session) ([]*schema.AgenticMessage, error)
	ListSessions(ctx context.Context, userID string, limit int) ([]string, error)
	DeleteSession(ctx context.Context, sessionID string) error
	GetMessages(ctx context.Context, userID, sessionID string, limit int) ([]*schema.AgenticMessage, error)
	AppendMessage(ctx context.Context, userID, sessionID string, message model.Message) error
	CreateSummary(ctx context.Context, userID, sessionID, summary string) error
	GetSummary(ctx context.Context, userID, sessionID string) (string, error)
}

type SessionManager struct {
	db    *gorm.DB
	limit int
}

// CreateSummary implements [Session]
// 将总结保存在冷数据库里面方便存储
func (s *SessionManager) CreateSummary(ctx context.Context, userID, sessionID, summary string) error {
	return s.db.Create(&model.Summary{
		UserID:    userID,
		SessionID: sessionID,
		Summary:   summary,
	}).Error
}

// GetSummary implements [Session]
// 从冷数据库中回复总结，选择最新的那一条
func (s *SessionManager) GetSummary(ctx context.Context, userID, sessionID string) (string, error) {
	var summary model.Summary
	err := s.db.Where("user_id = ?", userID).Where("session_id = ?", sessionID).First(&summary).Error
	return summary.Summary, err
}

// AppendMessage implements [Session].
// 向Session追加一条消息
func (s *SessionManager) AppendMessage(ctx context.Context, userID, sessionID string, message model.Message) error {
	err := s.db.Create(message).Error
	if err != nil {
		return errors.New("消息写入数据库失败" + err.Error())
	}
	return nil
}

// CreateSession implements [Session].
// 创建一个新的Session，当请求的session为空的时候，使用此方法，然后将session写入响应里面
func (s *SessionManager) CreateSession(session model.Session) ([]*schema.AgenticMessage, error) {
	err := s.db.Table("session").Create(session).Error
	if err != nil {
		return nil, err
	}
	return nil, nil
}

// DeleteSession implements [Session].
// 删掉一个Session，修改该Session为已删除的状态。
func (s *SessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	return s.db.Table("session").Update("status", false).Error
}

// GetMessages implements [Session].
// 获取当前Session的对话消息，首次只获取20次对话，
// 使用贪心算法，如果即将达到20条的限制之后，
// 会丢掉最远工具类的消息，直到最远的是用户类或者助手类的消息。
func (s *SessionManager) GetMessages(ctx context.Context, userID string, sessionID string, limit int) ([]*schema.AgenticMessage, error) {
	var session model.Session
	if err := s.db.Table("sesssion").Scan(&session).Error; err != nil {
		return nil, err
	}
	if !session.Status {
		return nil, fmt.Errorf("获取消息失败，会话%s已被删除", session.ID)
	}
	messages := make([]*schema.AgenticMessage, 0)
	err := s.db.Table("messages").Where("user_id = ?", userID).Where("session_id = ?", sessionID).Limit(limit).Scan(&messages).Error
	if err != nil {
		return nil, errors.New("获取消息失败" + err.Error())
	}
	return messages, nil
}

// ListSessions implements [Session].
// 列出所有的Session，放在对话的侧边，供用户的点击，前端进去默认是一个新的对话
func (s *SessionManager) ListSessions(ctx context.Context, userID string, limit int) ([]string, error) {
	sessions := make([]model.Session, 0)
	results := make([]string, 0)
	err := s.db.Table("sessions").Where("user_id = ?", userID).Scan(&sessions).Error
	if err != nil {
		return nil, errors.New("获取会话列表失败" + err.Error())
	}
	for _, session := range sessions {
		results = append(results, session.ID)
	}
	return results, nil
}

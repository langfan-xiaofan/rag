package memory

import (
	"context"
	"errors"
	"fmt"
	"rag/internal/model"
	"slices"

	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

// 该模块主要用于长期记忆的设计。

// ErrSessionDeleted 表示会话已被标记删除，不再接受新的对话。
var ErrSessionDeleted = errors.New("会话已被删除")

type Session interface {
	EnsureSession(ctx context.Context, userID uint, sessionID, title string) error
	CreateSession(ctx context.Context, session model.Session) error
	ListSessions(ctx context.Context, userID uint, limit int) ([]string, error)
	DeleteSession(ctx context.Context, userID uint, sessionID string) error
	GetMessages(ctx context.Context, userID uint, sessionID string, limit int) ([]*schema.Message, error)
	AppendMessage(ctx context.Context, userID uint, sessionID string, message model.Message) error
	CreateSummary(ctx context.Context, userID uint, sessionID, summary string) error
	GetSummary(ctx context.Context, userID uint, sessionID string) (string, error)
}

type SessionManager struct {
	db    *gorm.DB
	limit int
}

func NewSessionManager(db *gorm.DB, limit int) *SessionManager {
	return &SessionManager{
		db:    db,
		limit: limit,
	}
}

// EnsureSession implements [Session].
// 保证会话可用：不存在就当作新对话创建，已删除则拒绝继续。
func (s *SessionManager) EnsureSession(ctx context.Context, userID uint, sessionID, title string) error {
	var session model.Session
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", sessionID, userID).First(&session).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return s.CreateSession(ctx, model.Session{
			ID:     sessionID,
			UserID: userID,
			Title:  title,
		})
	case err != nil:
		return fmt.Errorf("查询会话失败: %w", err)
	case !session.Status:
		return ErrSessionDeleted
	}
	return nil
}

// CreateSession implements [Session].
// 创建一个新的Session，当请求的session为空的时候，使用此方法，然后将session写入响应里面
func (s *SessionManager) CreateSession(ctx context.Context, session model.Session) error {
	// Status 的零值是 false，也就是"已删除"，这里统一置为可用
	session.Status = true
	if err := s.db.WithContext(ctx).Create(&session).Error; err != nil {
		return fmt.Errorf("创建会话失败: %w", err)
	}
	return nil
}

// DeleteSession implements [Session].
// 删掉一个Session，修改该Session为已删除的状态。带上 userID，避免删掉别人的会话。
func (s *SessionManager) DeleteSession(ctx context.Context, userID uint, sessionID string) error {
	err := s.db.WithContext(ctx).Model(&model.Session{}).
		Where("id = ? AND user_id = ?", sessionID, userID).
		Update("status", false).Error
	if err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

// GetMessages implements [Session].
// 读取会话最近的 limit 条消息，按时间正序返回。
// 新会话查不到消息是正常的，返回空切片而不是错误。
func (s *SessionManager) GetMessages(ctx context.Context, userID uint, sessionID string, limit int) ([]*schema.Message, error) {
	if limit <= 0 {
		limit = s.limit
	}
	rows := make([]model.Message, 0, limit)
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND session_id = ?", userID, sessionID).
		Order("id DESC").Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("获取消息失败: %w", err)
	}
	slices.Reverse(rows) // 上面按 id 倒序取最近 limit 条，这里反转回时间正序
	rows = trimIncompleteToolCalls(rows)

	messages := make([]*schema.Message, 0, len(rows))
	for _, row := range rows {
		if row.Msg != nil {
			messages = append(messages, row.Msg)
		}
	}
	return messages, nil
}

// trimIncompleteToolCalls 去掉窗口两端残缺的工具调用消息。
// 截断出来的窗口如果以 tool 消息开头，或者以一条还没等到工具结果的 assistant 调用结尾，
// 模型 API 都会直接拒绝，所以把这两种边界丢掉。
func trimIncompleteToolCalls(rows []model.Message) []model.Message {
	start := 0
	for start < len(rows) && rows[start].Msg != nil && rows[start].Msg.Role == schema.Tool {
		start++
	}
	end := len(rows)
	for end > start {
		msg := rows[end-1].Msg
		if msg != nil && msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			end--
			continue
		}
		break
	}
	return rows[start:end]
}

// AppendMessage implements [Session].
// 向Session追加一条消息
func (s *SessionManager) AppendMessage(ctx context.Context, userID uint, sessionID string, message model.Message) error {
	message.UserID = userID
	message.SessionID = sessionID
	if err := s.db.WithContext(ctx).Create(&message).Error; err != nil {
		return fmt.Errorf("消息写入数据库失败: %w", err)
	}
	return nil
}

// CreateSummary implements [Session]
// 将总结保存在冷数据库里面方便存储
func (s *SessionManager) CreateSummary(ctx context.Context, userID uint, sessionID, summary string) error {
	err := s.db.WithContext(ctx).Create(&model.Summary{
		UserID:    userID,
		SessionID: sessionID,
		Summary:   summary,
	}).Error
	if err != nil {
		return fmt.Errorf("保存摘要失败: %w", err)
	}
	return nil
}

// GetSummary implements [Session]
// 从冷数据库中回复总结，选择最新的那一条；没有摘要不算错误。
func (s *SessionManager) GetSummary(ctx context.Context, userID uint, sessionID string) (string, error) {
	var summary model.Summary
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND session_id = ?", userID, sessionID).
		Order("id DESC").First(&summary).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("获取摘要失败: %w", err)
	}
	return summary.Summary, nil
}

// ListSessions implements [Session].
// 列出该用户未删除的Session，按创建时间倒序，放在对话的侧边，供用户的点击
func (s *SessionManager) ListSessions(ctx context.Context, userID uint, limit int) ([]string, error) {
	query := s.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, true).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	sessions := make([]model.Session, 0)
	if err := query.Find(&sessions).Error; err != nil {
		return nil, fmt.Errorf("获取会话列表失败: %w", err)
	}
	results := make([]string, 0, len(sessions))
	for _, session := range sessions {
		results = append(results, session.ID)
	}
	return results, nil
}

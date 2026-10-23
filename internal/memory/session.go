package memory

import (
	"context"
	"errors"
	"fmt"
	"rag/internal/model"
	"slices"
	"strings"

	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

// 该模块主要用于长期记忆的设计。

// ErrSessionDeleted 表示会话已被标记删除，不再接受新的对话。
var ErrSessionDeleted = errors.New("会话已被删除")

// ErrSessionKBConflict 表示本次提问选择的知识库和会话已绑定的知识库不一致。
// 不同知识库的历史不能混在一起，客户端应新建会话。
var ErrSessionKBConflict = errors.New("当前会话属于其他知识库，请新建会话")

type Session interface {
	EnsureSession(ctx context.Context, userID uint, sessionID string, kbID uint, title string) error
	CreateSession(ctx context.Context, session model.Session) error
	ListSessions(ctx context.Context, userID uint, limit int) ([]string, error)
	DeleteSession(ctx context.Context, userID uint, sessionID string) error
	GetMessages(ctx context.Context, userID uint, sessionID string, limit int) ([]*schema.Message, error)
	AppendMessage(ctx context.Context, userID uint, sessionID string, message model.Message) error
	CreateSummary(ctx context.Context, userID uint, sessionID, summary string, lastMessageID uint) error
	GetSummary(ctx context.Context, userID uint, sessionID string) (string, error)
	MaybeCompact(ctx context.Context, gen Summarizer, userID uint, sessionID string) error
}

type SessionManager struct {
	db           *gorm.DB
	limit        int // 上下文窗口：每轮读给模型的最近消息条数
	compactBatch int // 每次压缩挤进摘要的消息条数
}

func NewSessionManager(db *gorm.DB, limit, compactBatch int) *SessionManager {
	return &SessionManager{
		db:           db,
		limit:        limit,
		compactBatch: compactBatch,
	}
}

// EnsureSession implements [Session].
// 保证会话可用：不存在就当作新对话创建，已删除则拒绝继续；
// 会话已绑定知识库时，本次选择必须与之一致。
func (s *SessionManager) EnsureSession(ctx context.Context, userID uint, sessionID string, kbID uint, title string) error {
	var session model.Session
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", sessionID, userID).First(&session).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return s.CreateSession(ctx, model.Session{
			ID:              sessionID,
			UserID:          userID,
			KnowledgeBaseID: kbID,
			Title:           title,
		})
	case err != nil:
		return fmt.Errorf("查询会话失败: %w", err)
	case !session.Status:
		return ErrSessionDeleted
	case session.KnowledgeBaseID != 0 && session.KnowledgeBaseID != kbID:
		return ErrSessionKBConflict
	case session.KnowledgeBaseID == 0:
		// 升级前的老会话还没有绑定知识库，首次提问时补上
		if err := s.db.WithContext(ctx).Model(&model.Session{}).
			Where("id = ? AND user_id = ?", sessionID, userID).
			Update("knowledge_base_id", kbID).Error; err != nil {
			return fmt.Errorf("更新会话知识库失败: %w", err)
		}
	}

	// 通过 POST /v1/session 新建的会话标题是“新对话”，首次提问时用问题更新
	if session.Title == "" || session.Title == "新对话" {
		if t := sessionTitle(title); t != "" {
			if err := s.db.WithContext(ctx).Model(&model.Session{}).
				Where("id = ? AND user_id = ?", sessionID, userID).
				Update("title", t).Error; err != nil {
				return fmt.Errorf("更新会话标题失败: %w", err)
			}
		}
	}
	return nil
}

func sessionTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 30 {
		return string(runes[:30]) + "…"
	}
	return text
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
// 追加一版滚动摘要，并记下它已经覆盖到哪条消息（LastMessageID），供下次压缩当水位线。
func (s *SessionManager) CreateSummary(ctx context.Context, userID uint, sessionID, summary string, lastMessageID uint) error {
	err := s.db.WithContext(ctx).Create(&model.Summary{
		UserID:        userID,
		SessionID:     sessionID,
		Summary:       summary,
		LastMessageID: lastMessageID,
	}).Error
	if err != nil {
		return fmt.Errorf("保存摘要失败: %w", err)
	}
	return nil
}

// GetSummary implements [Session]
// 返回最新一版摘要的正文；没有摘要不算错误。
func (s *SessionManager) GetSummary(ctx context.Context, userID uint, sessionID string) (string, error) {
	text, _, err := s.summaryState(ctx, userID, sessionID)
	return text, err
}

// summaryState 取最新一版摘要的正文和水位线。没有摘要时返回空串和 0。
func (s *SessionManager) summaryState(ctx context.Context, userID uint, sessionID string) (string, uint, error) {
	var summary model.Summary
	// 用 Find 而不是 First：没有摘要时是正常情况，不应该让 GORM 打 record not found 日志
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND session_id = ?", userID, sessionID).
		Order("id DESC").Limit(1).Find(&summary).Error
	if err != nil {
		return "", 0, fmt.Errorf("获取摘要失败: %w", err)
	}
	if summary.ID == 0 {
		return "", 0, nil
	}
	return summary.Summary, summary.LastMessageID, nil
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

package service

import (
	"context"
	"errors"
	"time"

	"rag/internal/dao"
	"rag/internal/dto"
	"rag/internal/memory"
	"rag/internal/model"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrSessionNotFound = errors.New("会话不存在")

type SessionService struct {
	dao      *dao.SessionDao
	sessions *memory.SessionManager
	kb       *KnowledgeBaseService
}

func NewSessionService(db *gorm.DB, sessions *memory.SessionManager, kb *KnowledgeBaseService) *SessionService {
	return &SessionService{
		dao:      dao.NewSessionDao(db),
		sessions: sessions,
		kb:       kb,
	}
}

func (s *SessionService) Create(ctx context.Context, userID, kbID uint) (*dto.SessionRes, error) {
	kb, err := s.kb.GetOwned(ctx, userID, kbID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	session := model.Session{
		ID:              uuid.NewString(),
		UserID:          userID,
		KnowledgeBaseID: kb.ID,
		Title:           "新对话",
		CreatedAt:       now,
	}
	if err := s.sessions.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	return &dto.SessionRes{
		ID:                session.ID,
		Title:             session.Title,
		KnowledgeBaseID:   kb.ID,
		KnowledgeBaseName: kb.Name,
		CreatedAt:         now,
	}, nil
}

func (s *SessionService) List(ctx context.Context, userID uint) ([]dto.SessionRes, error) {
	rows, err := s.dao.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	res := make([]dto.SessionRes, 0, len(rows))
	for _, row := range rows {
		res = append(res, dto.SessionRes{
			ID:                row.ID,
			Title:             row.Title,
			KnowledgeBaseID:   row.KnowledgeBaseID,
			KnowledgeBaseName: row.KnowledgeBaseName,
			CreatedAt:         row.CreatedAt,
		})
	}
	return res, nil
}

// Messages 返回给前端展示的 user/assistant 文本，过滤掉工具调用和工具结果。
func (s *SessionService) Messages(ctx context.Context, userID uint, sessionID string) ([]dto.SessionMessageRes, error) {
	if _, err := s.getOwned(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	rows, err := s.dao.GetMessages(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	res := make([]dto.SessionMessageRes, 0, len(rows))
	for _, row := range rows {
		if row.Msg == nil {
			continue
		}
		if row.Msg.Role != schema.User && row.Msg.Role != schema.Assistant {
			continue
		}
		if row.Msg.Content == "" {
			continue
		}
		res = append(res, dto.SessionMessageRes{
			Role:      string(row.Msg.Role),
			Content:   row.Msg.Content,
			CreatedAt: row.CreatedAt,
		})
	}
	return res, nil
}

func (s *SessionService) Delete(ctx context.Context, userID uint, sessionID string) error {
	if _, err := s.getOwned(ctx, userID, sessionID); err != nil {
		return err
	}
	return s.dao.SoftDelete(ctx, sessionID, userID)
}

func (s *SessionService) getOwned(ctx context.Context, userID uint, sessionID string) (*model.Session, error) {
	session, err := s.dao.GetByIDAndUser(ctx, sessionID, userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

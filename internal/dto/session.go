package dto

import "time"

type CreateSessionReq struct {
	KnowledgeBaseID uint `json:"kb_id" binding:"required"`
}

type SessionRes struct {
	ID                string    `json:"id"`
	Title             string    `json:"title"`
	KnowledgeBaseID   uint      `json:"kb_id"`
	KnowledgeBaseName string    `json:"kb_name"`
	CreatedAt         time.Time `json:"created_at"`
}

type SessionMessageRes struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

package dto

import "time"

type CreateKBReq struct {
	Name        string `json:"name" binding:"required,max=128"`
	Description string `json:"description" binding:"max=512"`
}

// UpdateKBReq 用指针区分“没传”和“显式清空”。
type UpdateKBReq struct {
	Name        *string `json:"name" binding:"omitempty,max=128"`
	Description *string `json:"description" binding:"omitempty,max=512"`
}

type KBRes struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	FileCount   uint64    `json:"file_count"`
	ChunkCount  uint64    `json:"chunk_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type KBFileRes struct {
	FileName   string `json:"file_name"`
	FileType   string `json:"file_type"`
	Summary    string `json:"summary"`
	Key        string `json:"key"`
	UploadedAt string `json:"uploaded_at"`
}

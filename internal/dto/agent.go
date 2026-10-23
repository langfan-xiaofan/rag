package dto

type AskReq struct {
	Query           string `json:"query" binding:"required"`
	SessionID       string `json:"session_id"`
	KnowledgeBaseID uint   `json:"kb_id" binding:"required"`
}
type StreamChunk struct {
	Content string
	Err     error
	Done    bool
}

package dto

type AskReq struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id"`
}
type StreamChunk struct {
	Content string
	Err     error
	Done    bool
}

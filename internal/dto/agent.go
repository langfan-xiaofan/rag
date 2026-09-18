package dto

type AskReq struct {
	Query string `json:"query"`
}
type StreamChunk struct {
	Content string
	Err     error
	Done    bool
}

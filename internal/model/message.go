package model

import "github.com/cloudwego/eino/schema"

type Message struct {
	SessionID string
	Msg       *schema.AgenticMessage
}

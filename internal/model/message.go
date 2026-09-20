package model

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

type Message struct {
	ID        uint            `gorm:"primaryKey"`
	UserID    uint            `gorm:"index:idx_message_session,priority:1"`
	SessionID string          `gorm:"index:idx_message_session,priority:2;size:64"`
	Msg       *schema.Message `gorm:"serializer:json;type:json"`
	CreatedAt time.Time
}

package model

import "time"

type Summary struct {
	UserID    string
	SessionID string
	Summary   string
	CreatedAt time.Time
}

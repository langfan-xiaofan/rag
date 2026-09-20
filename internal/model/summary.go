package model

import "time"

type Summary struct {
	ID        uint   `gorm:"primaryKey"`
	UserID    uint   `gorm:"index:idx_summary_session,priority:1"`
	SessionID string `gorm:"index:idx_summary_session,priority:2;size:64"`
	Summary   string `gorm:"type:text"`
	CreatedAt time.Time
}

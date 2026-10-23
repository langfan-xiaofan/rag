package model

import "time"

type Summary struct {
	ID        uint   `gorm:"primaryKey"`
	UserID    uint   `gorm:"index:idx_summary_session,priority:1"`
	SessionID string `gorm:"index:idx_summary_session,priority:2;size:64"`
	Summary   string `gorm:"type:text"`
	// LastMessageID 是已经并进这份摘要的最大消息 ID。留给下次压缩当水位线，
	// 避免同一批消息被反复压进摘要（消息本身不会删，要留给前端回看）。
	LastMessageID uint
	CreatedAt     time.Time
}

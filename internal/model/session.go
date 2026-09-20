package model

import "time"

type Session struct {
	ID        string `gorm:"primaryKey;size:64"`
	UserID    uint   `gorm:"index"`
	Title     string `gorm:"size:255"`
	Status    bool
	CreatedAt time.Time
}

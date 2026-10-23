package model

import "time"

type Session struct {
	ID              string `gorm:"primaryKey;size:64"`
	UserID          uint   `gorm:"index"`
	KnowledgeBaseID uint   `gorm:"index;not null;default:0"`
	Title           string `gorm:"size:255"`
	Status          bool
	CreatedAt       time.Time
}

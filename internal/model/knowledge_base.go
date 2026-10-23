package model

import "time"

// KnowledgeBase 是一个用户下的独立知识库。
// DocCollection / FileCollection 保存 Qdrant 里实际的集合名，代码不按用户名拼集合名，
// 这样重命名知识库、兼容旧集合都不需要改数据。
type KnowledgeBase struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	UserID         uint      `gorm:"not null;index;uniqueIndex:idx_kb_user_name,priority:1" json:"user_id"`
	Name           string    `gorm:"size:128;not null;uniqueIndex:idx_kb_user_name,priority:2" json:"name"`
	Description    string    `gorm:"size:512" json:"description"`
	DocCollection  string    `gorm:"size:128;not null" json:"-"`
	FileCollection string    `gorm:"size:128;not null" json:"-"`
	ObjectPrefix   string    `gorm:"size:128;not null" json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

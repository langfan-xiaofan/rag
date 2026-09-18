package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/redis/go-redis/v9"
)

type Short interface {
	AppendMessage(ctx context.Context, sessionID string, message *schema.AgenticMessage) error
	GetMessages(ctx context.Context, sessionID string) ([]*schema.AgenticMessage, error)
	UpdateSummary(ctx context.Context, sessionID string, summary string) error
	GetSummary(ctx context.Context, sessionID string) (string, error)
	CreateSummary(ctx context.Context, sessionID string, summary string) error
}

type ShortMessageManager struct {
	redis  *redis.Client
	Window int
}

// AppendMessage implements [Short].
func (s *ShortMessageManager) AppendMessage(ctx context.Context, sessionID string, message *schema.AgenticMessage) error {
	pipe := s.redis.Pipeline()
	sessionID = sessionID + "chat"
	jsonData, _ := json.Marshal(message)
	pipe.RPush(ctx, sessionID, jsonData)
	pipe.LTrim(ctx, sessionID, -20, -1) //redis支持负数索引，最后一个为-1,倒数第二个为-2一次类推
	pipe.Expire(ctx, sessionID, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return errors.New("消息加入到redis失败" + err.Error())
	}
	return nil
}

// GetMessages implements [Short].
// 获取存在Redis里面的消息，Redis中对话的窗口默认大小为20
func (s *ShortMessageManager) GetMessages(ctx context.Context, sessionID string) ([]*schema.AgenticMessage, error) {
	messages := make([]*schema.AgenticMessage, 0)
	key := sessionID + "chat"
	values, err := s.redis.LRange(ctx, key, 1, 20).Result()
	if err != nil {
		return nil, fmt.Errorf("获取消息失败%s", err.Error())
	}
	for _, value := range values {
		var message *schema.AgenticMessage
		if err := json.Unmarshal([]byte(value), &message); err != nil {
			return nil, fmt.Errorf("获取消息失败%s", err.Error())
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// GetSummary implements [Short].
// 获取Summary，如果错误的话，直接返回空的就行。
func (s *ShortMessageManager) GetSummary(ctx context.Context, sessionID string) (string, error) {
	var summary string
	key := sessionID + "SUM"
	summary, _ = s.redis.Get(ctx, key).Result()
	return summary, nil
}

// UpdateSummary implements [Short].
// 更新Summary，更新完必须要删除队列里面的消息，如果发生了错误，那就不删掉，保存到下一次进行更改
func (s *ShortMessageManager) UpdateSummary(ctx context.Context, sessionID string, summary string) error {
	key := sessionID + "SUM"
	err := s.redis.Set(ctx, key, summary, 24*time.Hour).Err()
	if err != nil {
		return err
	}
	return s.redis.LTrim(ctx, sessionID+"chat", 20, -1).Err()
}

// CreateSummary implements [Short].
// 初次创建Summary，同样的写成功之后必须要清空这个Summary
func (s *ShortMessageManager) CreateSummary(ctx context.Context, sessionID string, summary string) error {
	key := sessionID + "SUM"
	err := s.redis.Set(ctx, key, summary, 24*time.Hour).Err()
	if err != nil {
		return err
	}
	return s.redis.LTrim(ctx, sessionID+"chat", 20, -1).Err()
}

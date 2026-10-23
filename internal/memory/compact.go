package memory

import (
	"context"
	"fmt"
	"strings"

	"rag/internal/model"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

// Summarizer 把「已有摘要 + 一批掉出窗口的消息」合成一版新摘要。
// 接口定义在消费方，方便换模型，也方便测试里塞一个假实现。
type Summarizer interface {
	Summarize(ctx context.Context, prev string, msgs []*schema.Message) (string, error)
}

// ChatSummarizer 用对话模型生成滚动摘要。
type ChatSummarizer struct {
	model *openai.ChatModel
}

func NewChatSummarizer(model *openai.ChatModel) *ChatSummarizer {
	return &ChatSummarizer{model: model}
}

const summarizePrompt = `你在为一段多轮对话维护滚动摘要，供后续轮次当背景使用。

要求：
1. 只记录事实：用户的身份/偏好/目标、已确认的结论、关键数据与出处、尚未解决的问题。
2. 不要记录寒暄，不要复述助手的长篇回答，不要写"用户提了一个问题"这类空话。
3. 给了【已有摘要】时，把新内容合并进去，输出一份完整的更新版摘要，而不是只写增量。
4. 直接输出摘要正文，不要标题、不要前后缀、不要解释。
5. 用中文，控制在 400 字以内。`

// Summarize implements [Summarizer]。
func (s *ChatSummarizer) Summarize(ctx context.Context, prev string, msgs []*schema.Message) (string, error) {
	var b strings.Builder
	if prev != "" {
		b.WriteString("【已有摘要】\n")
		b.WriteString(prev)
		b.WriteString("\n\n")
	}
	b.WriteString("【需要并入的新对话】\n")
	hasContent := false
	for _, m := range msgs {
		// 工具消息的 Content 通常是空的，且带 tool_call_id，这里只取人话
		if m == nil || m.Content == "" {
			continue
		}
		var role string
		switch m.Role {
		case schema.User:
			role = "用户"
		case schema.Assistant:
			role = "助手"
		default:
			continue
		}
		b.WriteString(role)
		b.WriteString("：")
		b.WriteString(m.Content)
		b.WriteString("\n")
		hasContent = true
	}
	if !hasContent {
		return "", nil
	}

	out, err := s.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(summarizePrompt),
		schema.UserMessage(b.String()),
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Content), nil
}

// MaybeCompact 把已经掉出上下文窗口、又还没进摘要的最旧一批消息压进滚动摘要。
//
// 这些消息仍留在 messages 表里供前端回看，所以不能靠删除来标记「已压过」——
// 而是用 Summary.LastMessageID 当水位线，否则同一批消息会被反复送进模型。
func (s *SessionManager) MaybeCompact(ctx context.Context, gen Summarizer, userID uint, sessionID string) error {
	if s.compactBatch <= 0 {
		return nil
	}

	var total int64
	if err := s.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ? AND session_id = ?", userID, sessionID).
		Count(&total).Error; err != nil {
		return fmt.Errorf("统计会话消息数失败: %w", err)
	}

	prev, lastID, err := s.summaryState(ctx, userID, sessionID)
	if err != nil {
		return err
	}

	var summarized int64
	if lastID > 0 {
		if err := s.db.WithContext(ctx).Model(&model.Message{}).
			Where("user_id = ? AND session_id = ? AND id <= ?", userID, sessionID, lastID).
			Count(&summarized).Error; err != nil {
			return fmt.Errorf("统计已压缩消息数失败: %w", err)
		}
	}

	// 窗口内的消息不动，只压缩已经掉出窗口的部分；攒够一批再动手，避免每轮都压。
	if total-int64(s.limit)-summarized < int64(s.compactBatch) {
		return nil
	}

	rows := make([]model.Message, 0, s.compactBatch)
	if err := s.db.WithContext(ctx).
		Where("user_id = ? AND session_id = ? AND id > ?", userID, sessionID, lastID).
		Order("id ASC").Limit(s.compactBatch).
		Find(&rows).Error; err != nil {
		return fmt.Errorf("读取待压缩消息失败: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	msgs := make([]*schema.Message, 0, len(rows))
	for _, row := range rows {
		if row.Msg != nil {
			msgs = append(msgs, row.Msg)
		}
	}
	if len(msgs) == 0 {
		return nil
	}

	next, err := gen.Summarize(ctx, prev, msgs)
	if err != nil {
		return fmt.Errorf("生成摘要失败: %w", err)
	}
	if next == "" {
		return nil
	}
	return s.CreateSummary(ctx, userID, sessionID, next, rows[len(rows)-1].ID)
}

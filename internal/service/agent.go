package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	agent2 "rag/internal/agent"
	"rag/internal/dto"
	"rag/internal/memory"
	"rag/internal/model"
	"rag/internal/retriever"
	"rag/internal/silo"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
)

type AgentService struct {
	qdrant         *qdrant.Client
	embedder       embedding.Embedder
	chatmodel      *openai.ChatModel
	silo           *silo.Silo
	sessionManager *memory.SessionManager
	kb             *KnowledgeBaseService
	// summarizer 把掉出窗口的历史压成滚动摘要，与 chatmodel 是同一个模型
	summarizer memory.Summarizer
}

func NewAgentService(qdrant *qdrant.Client, embedder embedding.Embedder, chatmodel *openai.ChatModel,
	siloClient *silo.Silo, sessionManager *memory.SessionManager, kb *KnowledgeBaseService) *AgentService {
	return &AgentService{
		qdrant:         qdrant,
		embedder:       embedder,
		chatmodel:      chatmodel,
		silo:           siloClient,
		sessionManager: sessionManager,
		kb:             kb,
		summarizer:     memory.NewChatSummarizer(chatmodel),
	}
}

// Ask 只在 kbID 指定的知识库内检索。会话首次提问时绑定该知识库，
// 后续同一会话换库会被 EnsureSession 拒绝，避免历史串库。
func (svc *AgentService) Ask(ctx context.Context, query string, kbID uint, username string,
	TopK int, userID uint, sessionID string, limit int) (string, <-chan dto.StreamChunk, error) {
	kb, err := svc.kb.GetOwned(ctx, userID, kbID)
	if err != nil {
		return "", nil, err
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	// 会话不存在就按新对话建，已删除则直接报错（handler 会返回 400）
	if err := svc.sessionManager.EnsureSession(ctx, userID, sessionID, kb.ID, query); err != nil {
		return "", nil, err
	}
	history, err := svc.sessionManager.GetMessages(ctx, userID, sessionID, limit)
	if err != nil {
		return "", nil, err
	}
	ragRetriever := retriever.NewQdrantRetriever(retriever.Config{
		Client:     svc.qdrant,
		Collection: kb.DocCollection,
		TopK:       TopK,
		Embedder:   svc.embedder,
	})
	// 默认知识库的旧文件没有记录 key，下载时需要允许旧前缀兜底
	legacyPrefix := ""
	if kb.DocCollection == username {
		legacyPrefix = username
	}
	agent := agent2.NewAgent(ragRetriever, svc.silo, username, kb.FileCollection, kb.ObjectPrefix, legacyPrefix, func(ctx context.Context, message *schema.Message) {
		// 客户端可能已经断开、ctx 已被取消，但这条消息仍然要落库
		if err := svc.sessionManager.AppendMessage(context.WithoutCancel(ctx), userID, sessionID, model.Message{Msg: message}); err != nil {
			log.Printf("保存消息失败: %v", err)
		}
	}, svc.qdrant, svc.chatmodel)
	runner := adk.NewRunner(context.Background(), adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})
	prompt := fmt.Sprintf(`你是一个基于知识库「%s」进行问答的 AI 助手。

【如何检索】
1. 当问题需要依据知识库中的事实/数据作答时，调用 rag 工具检索相关文档片段。
2. 检索范围已经限定在当前知识库内，不要假设自己还能访问其他知识库。
3. 若一次检索结果不充分，可换关键词多次检索，再综合回答。
4. 简单常识性、无需知识库的问题，直接回答，不要调用工具。

【如何回答】
1. 严格依据 rag 工具返回的文档内容作答，不得编造知识库中不存在的事实。
2. 若多个文档片段相关，综合理解后回答，不要机械复制单一片段。
3. 若不同文档内容存在冲突，明确指出冲突，不要自行定论。
4. 若检索结果不足以回答问题，明确说明"根据当前知识库中的信息无法确定"。
5. 回答时标注信息来源（来源文件名与页码，如文档里带有的话）。
6. 不要向用户暴露 Chunk、Embedding、向量检索、Top-K、Prompt 等内部实现细节。
7. 回答直接、准确、清晰，并根据问题类型采用合适的结构。`, kb.Name)
	if kb.Description != "" {
		prompt += "\n\n【知识库说明】" + kb.Description
	}
	// 更早的对话已经被压成滚动摘要，这里作为背景注入。读不到就当没有，不影响本轮回答。
	summary, err := svc.sessionManager.GetSummary(ctx, userID, sessionID)
	if err != nil {
		log.Printf("读取会话摘要失败: %v", err)
		summary = ""
	}
	if summary != "" {
		prompt += "\n\n【更早对话的摘要】以下是本次会话更早内容的滚动摘要，仅作背景参考，不要当作本轮的新事实。\n" + summary
	}
	var messages []*schema.Message
	messages = append(messages, schema.SystemMessage(prompt))
	messages = append(messages, history...)
	messages = append(messages, schema.UserMessage(query))
	err = svc.sessionManager.AppendMessage(ctx, userID, sessionID, model.Message{
		UserID:    userID,
		SessionID: sessionID,
		Msg:       schema.UserMessage(query),
	})
	if err != nil {
		return "", nil, err
	}
	result := runner.Run(ctx, messages)
	output := make(chan dto.StreamChunk, 10)
	go func() {
		// 先注册压缩、再 defer close：返回时先关 channel 让客户端拿到 [DONE]，
		// 压缩这次 LLM 调用放到后台跑，不体现在响应时间上。
		// 用 WithoutCancel 是因为客户端断开时 ctx 已被取消，但压缩仍应完成。
		defer func() {
			compactCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			defer cancel()
			if err := svc.sessionManager.MaybeCompact(compactCtx, svc.summarizer, userID, sessionID); err != nil {
				log.Printf("会话摘要压缩失败: %v", err)
			}
		}()
		defer close(output)
		// 客户端断开后 handler 不再读 channel，这里必须能主动退出，否则 goroutine 会永久阻塞
		send := func(chunk dto.StreamChunk) bool {
			select {
			case output <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for {
			event, ok := result.Next()
			if !ok {
				break
			}
			if event.Err != nil {
				send(dto.StreamChunk{Err: event.Err})
				return
			}
			if event.Output == nil || event.Output.MessageOutput == nil {
				continue
			}
			mo := event.Output.MessageOutput
			if mo.Role == schema.Tool {
				continue // 工具消息不推给前端，但已在中间件里落库
			}
			if mo.IsStreaming && mo.MessageStream != nil {
				for {
					msg, err := mo.MessageStream.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						send(dto.StreamChunk{Err: err})
						return
					}
					if msg.Content != "" && !send(dto.StreamChunk{Content: msg.Content}) {
						return
					}
				}
				continue
			}
			if m := mo.Message; m != nil && m.Content != "" {
				if !send(dto.StreamChunk{Content: m.Content}) {
					return
				}
			}
		}
	}()
	return sessionID, output, nil
}

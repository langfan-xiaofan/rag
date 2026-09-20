package service

import (
	"context"
	"errors"
	"io"
	"log"
	agent2 "rag/internal/agent"
	"rag/internal/dto"
	"rag/internal/memory"
	"rag/internal/model"
	"rag/internal/retriever"
	"rag/internal/silo"

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
}

func NewAgentService(qdrant *qdrant.Client, embedder embedding.Embedder, chatmodel *openai.ChatModel, siloClient *silo.Silo, sessionManager *memory.SessionManager) *AgentService {
	return &AgentService{
		qdrant:         qdrant,
		embedder:       embedder,
		chatmodel:      chatmodel,
		silo:           siloClient,
		sessionManager: sessionManager,
	}
}

func (svc *AgentService) Ask(ctx context.Context, query string, username string, TopK int, userID uint, sessionID string, limit int) (string, <-chan dto.StreamChunk, error) {
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	// 会话不存在就按新对话建，已删除则直接报错（handler 会返回 400）
	if err := svc.sessionManager.EnsureSession(ctx, userID, sessionID, query); err != nil {
		return "", nil, err
	}
	history, err := svc.sessionManager.GetMessages(ctx, userID, sessionID, limit)
	if err != nil {
		return "", nil, err
	}
	ragRetriever := retriever.NewQdrantRetriever(retriever.Config{
		Client:     svc.qdrant,
		Collection: username,
		TopK:       TopK,
		Embedder:   svc.embedder,
	})
	fileRetriever := retriever.NewQdrantRetriever(retriever.Config{
		Client:     svc.qdrant,
		Collection: username + "file",
		TopK:       TopK,
		Embedder:   svc.embedder,
	})
	agent := agent2.NewAgent(ragRetriever, fileRetriever, svc.silo, username, func(ctx context.Context, message *schema.Message) {
		// 客户端可能已经断开、ctx 已被取消，但这条消息仍然要落库
		if err := svc.sessionManager.AppendMessage(context.WithoutCancel(ctx), userID, sessionID, model.Message{Msg: message}); err != nil {
			log.Printf("保存消息失败: %v", err)
		}
	})
	runner := adk.NewRunner(context.Background(), adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})
	//docs, err := re.Retrieve(context.Background(), query)
	//if err != nil {
	//	return nil, errors.New("检索失败" + err.Error())
	//}
	prompt := `你是一个基于企业知识库进行问答的 AI 助手。                                                                                                                                           
                                                                                                                                                                                                        
     【如何检索】                                                                                                                                                                                       
     1. 当问题需要依据知识库中的事实/数据作答时，调用 rag_search 工具检索相关文档片段。                                                                                                                 
     2. 若一次检索结果不充分，可换关键词多次检索，再综合回答。                                                                                                                                          
     3. 简单常识性、无需知识库的问题，直接回答，不要调用工具。                                                                                                                                          
                                                                                                                                                                                                        
     【如何回答】                                                                                                                                                                                       
     1. 严格依据 rag_search 返回的文档内容作答，不得编造知识库中不存在的事实。                                                                                                                          
     2. 若多个文档片段相关，综合理解后回答，不要机械复制单一片段。                                                                                                                                      
     3. 若不同文档内容存在冲突，明确指出冲突，不要自行定论。                                                                                                                                            
     4. 若检索结果不足以回答问题，明确说明"根据当前知识库中的信息无法确定"。                                                                                                                            
     5. 回答时标注信息来源（来源文件名与页码，如文档里带有的话）。                                                                                                                                      
     6. 不要向用户暴露 Chunk、Embedding、向量检索、Top-K、Prompt 等内部实现细节。                                                                                                                       
     7. 回答直接、准确、清晰，并根据问题类型采用合适的结构。`
	//var buff strings.Builder
	//for _, doc := range docs {
	//	buff.WriteString(doc.Content)
	//}
	//context := buff.String()
	//prompt := fmt.Sprintf(prompttemp, context)
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

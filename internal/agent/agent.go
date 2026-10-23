package agent

import (
	"context"
	"log"
	"os"
	"rag/internal/silo"
	"rag/internal/tools"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/qdrant/go-client/qdrant"
)

type Handler struct {
	*adk.BaseChatModelAgentMiddleware
	onMessage func(ctx context.Context, message *schema.Message) //存储上下文的闭包函数，里面鞋带了UserID，sessionID等信息。
}

func NewHandler(onMessage func(ctx context.Context, message *schema.Message)) *Handler {
	return &Handler{
		onMessage: onMessage,
	}
}

func (h *Handler) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	runCtx.Instruction += "\n请始终按照最新的一条消息的语言进行回复"
	return ctx, runCtx, nil
}

func (h *Handler) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if len(state.Messages) == 0 {
		return ctx, state, nil
	}
	// 只落库本轮模型新产出的那一条：Content 为空但带 ToolCalls 的也要存，
	// 它是后面那条 tool 消息的另一半
	last := state.Messages[len(state.Messages)-1]
	if last == nil || (last.Content == "" && len(last.ToolCalls) == 0) {
		return ctx, state, nil
	}
	h.onMessage(ctx, last)
	return ctx, state, nil
}

func NewAgent(ragRetriever retriever.Retriever, siloClient *silo.Silo, bucket, fileCollection, objectPrefix, legacyPrefix string,
	onMessage func(ctx context.Context, message *schema.Message), qdrantClient *qdrant.Client,
	chatModel *openai.ChatModel) adk.ResumableAgent {
	if chatModel == nil {
		var err error
		chatModel, err = openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
			BaseURL: os.Getenv("DEEPSEEK_BASE_URL"),
			APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
			Model:   os.Getenv("DEEPSEEK_MODEL_NAME"),
		})
		if err != nil {
			panic(err)
		}
	}
	agent, _ := adk.NewTypedChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Model:       chatModel,
		Name:        "agent",
		Description: "你是一个全能的agent",
		ToolsConfig: adk.ToolsConfig{
			EmitInternalEvents: true,
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{
					tools.NewRagTool(ragRetriever),
					tools.GetFileObjectTool(siloClient, bucket, fileCollection, objectPrefix, legacyPrefix, qdrantClient),
					tools.NewListFileTool(context.Background(), fileCollection, qdrantClient),
					tools.RagSearchByFileName(ragRetriever),
				},
				ToolCallMiddlewares: []compose.ToolMiddleware{
					{
						Invokable: func(toolEndpoint compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
							return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
								output, err := toolEndpoint(ctx, input)
								if err != nil {
									// 工具失败也必须补一条 tool 消息，否则助手那条带 tool_calls 的消息会成为孤儿，
									// 之后每一轮请求都会被模型 API 以「tool_calls 没有对应的 tool 消息」拒绝。
									// 注意：失败时 output 恒为 nil（eino compose/tool_node.go 的 wrapToolCall 直接
									// return nil, err），所以只能拿 err 当工具结果，不能碰 output。
									onMessage(ctx, schema.ToolMessage("工具调用失败："+err.Error(), input.CallID, schema.WithToolName(input.Name)))
									log.Printf("调用工具 %s 失败: %v\n", input.Name, err)
									return &compose.ToolOutput{Result: "工具调用失败:" + err.Error()}, nil
								}
								onMessage(ctx, schema.ToolMessage(output.Result, input.CallID, schema.WithToolName(input.Name))) //利用闭包函数存储工具类型的消息。
								log.Printf("调用工具 %s: %v\n", input.Name, input.Arguments)
								log.Printf("工具结果: %v\n", output.Result)
								return output, nil
							}
						},
					},
				},
			},
		},
		Handlers: []adk.TypedChatModelAgentMiddleware[*schema.Message]{
			NewHandler(onMessage),
		},
		MaxIterations: 10000,
	})
	return agent
}

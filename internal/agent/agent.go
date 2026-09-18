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
)

type Handler struct {
	*adk.BaseChatModelAgentMiddleware
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	runCtx.Instruction += "\n请始终按照最新的一条消息的语言进行回复"
	return ctx, runCtx, nil
}

func NewAgent(ragRetriever, fileRetriever retriever.Retriever, silo *silo.Silo, bucket string) adk.ResumableAgent {
	chatmodel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: os.Getenv("DEEPSEEK_BASE_URL"),
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		Model:   os.Getenv("DEEPSEEK_MODEL_NAME"),
	})
	if err != nil {
		panic(err)
	}
	agent, _ := adk.NewTypedChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Model:       chatmodel,
		Name:        "agent",
		Description: "你是一个全能的agent",
		ToolsConfig: adk.ToolsConfig{
			EmitInternalEvents: true,
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{
					tools.NewRagTool(ragRetriever),
					tools.GetFilesTool(silo, bucket),
					tools.NewGetFilesNameTool(fileRetriever),
				},
				ToolCallMiddlewares: []compose.ToolMiddleware{
					{
						Invokable: func(toolEndpoint compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
							return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
								output, err := toolEndpoint(ctx, input)
								if err != nil {
									return nil, err
								}
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
			NewHandler(),
		},
		MaxIterations: 10000,
	})
	return agent
}

package tools

import (
	"context"
	"strconv"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type RagInput struct {
	Question string `json:"question"`
	TopK     string `json:"top_k"`
}

type RagOutput struct {
	Content  []string                 `json:"content"`
	MetaData []map[string]interface{} `json:"meta_data"`
}

func NewRagTool(re retriever.Retriever) tool.InvokableTool {
	return utils.NewTool(
		&schema.ToolInfo{
			Name: "rag",
			Desc: "当回答需要基于知识库中的事实信息时，用它检索相关文档片段。仅当问题涉及知识库内容时才调用。",
			ParamsOneOf: schema.NewParamsOneOfByParams(
				map[string]*schema.ParameterInfo{
					"question": {
						Type:     "string",
						Desc:     "用户需要查询的问题",
						Required: true,
					},
					"top_k": {
						Type:     "string",
						Required: true,
						Desc:     "返回的知识库的条数",
					},
				}),
		}, func(ctx context.Context, input RagInput) (output RagOutput, err error) {
			topK, _ := strconv.Atoi(input.TopK)
			documents, err := re.Retrieve(ctx, input.Question, retriever.WithTopK(topK))
			if err != nil {
				return RagOutput{}, err
			}
			output = RagOutput{}
			for _, document := range documents {
				output.Content = append(output.Content, document.Content)
				if document != nil {
					output.MetaData = append(output.MetaData, document.MetaData)
				}
			}
			return output, nil
		})
}

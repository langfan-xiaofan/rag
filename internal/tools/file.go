package tools

import (
	"context"
	"path/filepath"
	"rag/internal/silo"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type ListFileInput struct {
	FileName string `json:"file_name"`
}

type FileInfo struct {
	FileName string
	FileType string
	Bucket   string
}

type ListFileOutput struct {
	Files []FileInfo `json:"file_name"`
}

func NewGetFilesNameTool(re eino.Retriever) tool.InvokableTool {
	return utils.NewTool(&schema.ToolInfo{
		Name: "list_file",
		Desc: ` • 什么时候用：用户问"我上传了哪些文件""有没有关于 X 的文件"                                                                                                                                          
   • 返回什么：匹配到的文件名列表（不含文件内容）                                                                                                                                                       
   • 之后做什么：用户要文件本体时再调 get_files `,
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{
				"filename": {
					Type:     "string",
					Required: true,
					Desc:     "用户输入的文件名",
				},
			},
		),
	}, func(ctx context.Context, input ListFileInput) (output ListFileOutput, err error) {
		output = ListFileOutput{Files: []FileInfo{}}
		documents, err := re.Retrieve(ctx, input.FileName)
		if err != nil {
			return output, err
		}
		for _, document := range documents {
			if metaString(document.MetaData, "filename") == "" {
				continue
			}
			output.Files = append(output.Files, FileInfo{
				FileName: metaString(document.MetaData, "filename"),
				FileType: metaString(document.MetaData, "filetype"),
				Bucket:   metaString(document.MetaData, "bucket"),
			})
		}
		return output, nil
	})
}

type GetFileInput struct {
	FileName string `json:"file_name"`
}

type GetFileOutput struct {
	FileName string
	URL      string
	FileType string
}

func GetFilesTool(silo *silo.Silo, bucket string) tool.InvokableTool {
	return utils.NewTool(
		&schema.ToolInfo{
			Name: "get_files",
			Desc: "获取文件的本体",
			ParamsOneOf: schema.NewParamsOneOfByParams(
				map[string]*schema.ParameterInfo{
					"file_name": {
						Type:     "string",
						Desc:     "单一文件的名称",
						Required: true,
					},
				},
			),
		}, func(ctx context.Context, input GetFileInput) (output GetFileOutput, err error) {
			output = GetFileOutput{FileName: input.FileName}
			url, err := silo.GetFilePreSignUrl(input.FileName, bucket)
			if err != nil {
				return GetFileOutput{
					URL:      url,
					FileName: input.FileName,
					FileType: filepath.Ext(input.FileName),
				}, err
			}
			output.URL = url
			return output, nil
		},
	)
}

func metaString(m map[string]any, key string) string {
	s, ok := m[key].(string)
	if ok {
		return s
	}
	return ""
}

package tools

import (
	"context"
	"errors"
	"log"
	"path"
	"path/filepath"
	"strings"

	"rag/internal/silo"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fileCollectionSuffix 与 ingest 的文件记录集合保持一致：记录进 {username}file。
const fileCollectionSuffix = "file"

type ListFileInput struct {
	FileName string `json:"file_name"`
}

type FileInfo struct {
	FileName string
	FileType string
	Bucket   string
	Summary  string
	// Key 是对象在桶里的完整路径，get_files 直接拿它签下载链接。
	Key string
}

type ListFileOutput struct {
	Note  string     `json:"note"`
	Files []FileInfo `json:"files"`
}

func NewListFileTool(ctx context.Context, username string, qdrantClient *qdrant.Client) tool.InvokableTool {
	return utils.NewTool(&schema.ToolInfo{
		Name: "list_file",
		Desc: ` • 什么时候用：用户问"我上传了哪些文件""有没有关于 X 的文件"                                                                                                                                          
   • 返回什么：匹配到的文件名列表（不含文件内容）                                                                                                                                                       
   • 之后做什么：用户要文件本体时再调 get_files `,
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{},
		),
	}, func(ctx context.Context, input struct{}) (output ListFileOutput, err error) {
		output.Files = make([]FileInfo, 0)
		pts, err := qdrantClient.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: username + fileCollectionSuffix,
			WithPayload:    qdrant.NewWithPayload(true),
			Limit:          new(uint32(200)),
		})
		if err != nil {
			if status.Code(err) == codes.NotFound {
				output.Note = "该账号还未上传任何文件"
				err = nil
				return
			}
			return
		}
		for _, pt := range pts {
			output.Files = append(output.Files, FileInfo{
				FileName: pt.GetPayload()["filename"].GetStringValue(),
				FileType: pt.GetPayload()["filetype"].GetStringValue(),
				Bucket:   pt.GetPayload()["bucket"].GetStringValue(),
				Summary:  pt.GetPayload()["summary"].GetStringValue(),
				Key:      pt.GetPayload()["key"].GetStringValue(),
			})
		}
		return
	})
}

//func NewGetFilesNameTool(re eino.Retriever) tool.InvokableTool {
//	return utils.NewTool(&schema.ToolInfo{
//		Name: "list_file",
//		Desc: ` • 什么时候用：用户问"我上传了哪些文件""有没有关于 X 的文件"
//   • 返回什么：匹配到的文件名列表（不含文件内容）
//   • 之后做什么：用户要文件本体时再调 get_files `,
//		ParamsOneOf: schema.NewParamsOneOfByParams(
//			map[string]*schema.ParameterInfo{
//				"filename": {
//					Type:     "string",
//					Required: true,
//					Desc:     "用户输入的文件名",
//				},
//			},
//		),
//	}, func(ctx context.Context, input ListFileInput) (output ListFileOutput, err error) {
//		output = ListFileOutput{Files: []FileInfo{}}
//		documents, err := re.Retrieve(ctx, input.FileName)
//		if err != nil {
//			return output, err
//		}
//		for _, document := range documents {
//			if metaString(document.MetaData, "filename") == "" {
//				continue
//			}
//			output.Files = append(output.Files, FileInfo{
//				FileName: metaString(document.MetaData, "filename"),
//				FileType: metaString(document.MetaData, "filetype"),
//				Bucket:   metaString(document.MetaData, "bucket"),
//			})
//		}
//		return output, nil
//	})
//}

type GetFileInput struct {
	FileName string `json:"file_name"`
}

type GetFileOutput struct {
	FileName string
	URL      string
	FileType string
	// Key 是实际签名的对象 key，链接打不开时方便对着看。
	Key string
}

func GetFileObjectTool(silo *silo.Silo, bucket, username string, qdrantClient *qdrant.Client) tool.InvokableTool {
	return utils.NewTool(
		&schema.ToolInfo{
			Name: "get_files",
			Desc: "获取文件的本体，返回可直接下载的预签名 URL。file_name 传 list_file 给出的文件名或 Key。",
			ParamsOneOf: schema.NewParamsOneOfByParams(
				map[string]*schema.ParameterInfo{
					"file_name": {
						Type:     "string",
						Desc:     "单一文件的名称（list_file 返回的 filename 或 key）",
						Required: true,
					},
				},
			),
		}, func(ctx context.Context, input GetFileInput) (output GetFileOutput, err error) {
			name := strings.TrimSpace(input.FileName)
			output = GetFileOutput{FileName: name, FileType: filepath.Ext(name)}
			if name == "" {
				return output, errors.New("file_name 不能为空")
			}
			output.Key, err = resolveObjectKey(ctx, qdrantClient, username, name, silo, bucket)
			if err != nil {
				return output, err
			}
			output.URL, err = silo.GetFilePreSignUrl(output.Key, bucket)
			if err != nil {
				return output, err
			}
			return output, nil
		},
	)
}

// resolveObjectKey 定位文件在桶里真实存在的 key：先看文件记录里存的 key，
// 再退回裸文件名，最后按文件名在桶里兜底扫。早期版本把对象存成「前缀/文件名」，
// 却只按文件名签链接，于是点开就是 NoSuchKey。
func resolveObjectKey(ctx context.Context, qdrantClient *qdrant.Client, username, name string, silo *silo.Silo, bucket string) (string, error) {
	candidates := make([]string, 0, 3)
	if key := recordObjectKey(ctx, qdrantClient, username, name); key != "" {
		candidates = append(candidates, key)
	}
	return silo.ResolveKey(bucket, name, append(candidates, name, path.Base(name))...)
}

// recordObjectKey 取出这条文件记录里存的 key。
// 记录不存在（图片不写记录、老数据、记录被删）时返回空串，交给调用方兜底。
func recordObjectKey(ctx context.Context, qdrantClient *qdrant.Client, username, name string) string {
	pts, err := qdrantClient.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: username + fileCollectionSuffix,
		Filter: &qdrant.Filter{
			Should: []*qdrant.Condition{
				qdrant.NewMatch("filename", name),
				qdrant.NewMatch("key", name),
			},
		},
		WithPayload: qdrant.NewWithPayload(true),
		Limit:       new(uint32(8)),
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ""
		}
		log.Printf("查询文件记录失败，改用文件名兜底: %v", err)
		return ""
	}
	for _, pt := range pts {
		if key := pt.GetPayload()["key"].GetStringValue(); key != "" {
			return key
		}
	}
	return ""
}

func metaString(m map[string]any, key string) string {
	s, ok := m[key].(string)
	if ok {
		return s
	}
	return ""
}

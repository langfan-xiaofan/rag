package tools

import (
	"context"
	"errors"
	"io"
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

func NewListFileTool(ctx context.Context, fileCollection string, qdrantClient *qdrant.Client) tool.InvokableTool {
	return utils.NewTool(&schema.ToolInfo{
		Name: "list_file",
		Desc: ` • 什么时候用：用户问"当前知识库里有哪些文件""有没有关于 X 的文件"
   • 返回什么：当前知识库匹配到的文件名列表（不含文件内容）
   • 之后做什么：用户要文件本体时再调 get_files`,
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{},
		),
	}, func(ctx context.Context, input struct{}) (output ListFileOutput, err error) {
		output.Files = make([]FileInfo, 0)
		exists, err := qdrantClient.CollectionExists(ctx, fileCollection)
		if err != nil {
			return output, err
		}
		if !exists {
			output.Note = "该知识库还未上传任何文件"
			return output, nil
		}

		it := qdrantClient.ScrollAll(ctx, &qdrant.ScrollPoints{
			CollectionName: fileCollection,
			WithPayload:    qdrant.NewWithPayload(true),
			Limit:          new(uint32(256)),
		})
		for {
			pts, err := it.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				if status.Code(err) == codes.NotFound {
					output.Note = "该知识库还未上传任何文件"
					return output, nil
				}
				return output, err
			}
			for _, pt := range pts {
				payload := pt.GetPayload()
				fileName := payload["filename"].GetStringValue()
				fileType := payload["filetype"].GetStringValue()
				if fileType == "" {
					// 早期文件记录没有 filetype，按扩展名补一个展示值
					fileType = strings.TrimPrefix(strings.ToLower(path.Ext(fileName)), ".")
				}
				output.Files = append(output.Files, FileInfo{
					FileName: fileName,
					FileType: fileType,
					Bucket:   payload["bucket"].GetStringValue(),
					Summary:  payload["summary"].GetStringValue(),
					Key:      payload["key"].GetStringValue(),
				})
			}
		}
		return output, nil
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

func GetFileObjectTool(silo *silo.Silo, bucket, fileCollection, objectPrefix, legacyPrefix string, qdrantClient *qdrant.Client) tool.InvokableTool {
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
			output.Key, err = resolveObjectKey(ctx, qdrantClient, fileCollection, objectPrefix, legacyPrefix, name, silo, bucket)
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

// resolveObjectKey 只允许定位当前知识库里的对象：
//   - 先看文件记录里存的 key；
//   - 再试知识库前缀下的同名文件；
//   - 默认知识库额外允许旧前缀（username/）和桶根目录；
//   - 最后对默认知识库做一次排除 kb_ 前缀的旧目录兜底，避免命中其他知识库。
func resolveObjectKey(ctx context.Context, qdrantClient *qdrant.Client, fileCollection, objectPrefix, legacyPrefix, name string, silo *silo.Silo, bucket string) (string, error) {
	candidates := make([]string, 0, 5)
	if key := recordObjectKey(ctx, qdrantClient, fileCollection, name); key != "" {
		candidates = append(candidates, key)
	}
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if objectPrefix != "" {
		candidates = append(candidates, path.Join(objectPrefix, base))
	}
	if legacyPrefix != "" {
		candidates = append(candidates, path.Join(legacyPrefix, base), base)
	}
	key, err := silo.ResolveKeyWithin(bucket, candidates...)
	if err == nil {
		return key, nil
	}
	if legacyPrefix != "" {
		return silo.ResolveLegacyKey(bucket, base)
	}
	return "", err
}

// recordObjectKey 取出这条文件记录里存的 key。
// 记录不存在（图片不写记录、老数据、记录被删）时返回空串，交给调用方兜底。
func recordObjectKey(ctx context.Context, qdrantClient *qdrant.Client, fileCollection, name string) string {
	exists, err := qdrantClient.CollectionExists(ctx, fileCollection)
	if err != nil || !exists {
		return ""
	}
	pts, err := qdrantClient.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: fileCollection,
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

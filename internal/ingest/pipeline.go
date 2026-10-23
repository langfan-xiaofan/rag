// Package ingest 把"一个文件从对象存储走到向量库"的全过程收敛到一条流水线。
// 它只关心技术步骤（解析、切分、向量化、入库），不涉及 HTTP 与用户概念，
// 因此这里的组件都是无状态的，整个进程构造一次即可复用。
package ingest

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rag/internal/indexer"
	"rag/internal/parser"
	"rag/internal/silo"
	"rag/internal/transformer"

	"github.com/cloudwego/eino-ext/components/model/openai"
	einoparser "github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// FileCleaner 在写入新版本前清理同一知识库里同名文件的旧分块和旧记录。
// 由 service.KnowledgeBaseService 实现，避免 ingest 反向依赖知识库模型。
type FileCleaner interface {
	DeleteFileVectors(ctx context.Context, docCollection, fileCollection, fileName string) error
}

type Config struct {
	// Parser 由 internal/parser.NewExtParser() 构造，类型是 eino 的 ExtParser
	Parser        *einoparser.ExtParser
	Transformer   *transformer.Transformer
	Indexer       *indexer.QdrantIndexer
	Silo          *silo.Silo
	TextEmbedder  embedding.Embedder
	MultiEmbedder embedding.Embedder
	ChatModel     *openai.ChatModel
	Cleaner       FileCleaner
}

type Pipeline struct {
	cfg Config
}

func New(cfg *Config) *Pipeline {
	return &Pipeline{cfg: *cfg}
}

// Target 是每次调用携带的知识库上下文：集合名、对象存储前缀都由知识库记录决定。
type Target struct {
	UserID          uint
	Username        string
	KnowledgeBaseID uint
	DocCollection   string
	FileCollection  string
	ObjectPrefix    string
	Prefix          string
}

// StoragePrefix 返回对象在桶里的最终前缀（知识库前缀 + 用户填写的归档目录）。
func (t Target) StoragePrefix() string {
	switch {
	case t.ObjectPrefix == "":
		return t.Prefix
	case t.Prefix == "":
		return t.ObjectPrefix
	default:
		return t.ObjectPrefix + "/" + t.Prefix
	}
}

// IngestFile 走完整条流水线：
//  1. 打开文件并嗅探 MIME 类型
//  2. 上传对象存储（bucket = Username，key = ObjectPrefix/[Prefix/]Filename）
//  3. 图片走多模态嵌入；其余走 解析 → 切分 → 逐块文本嵌入
//  4. 清理同一知识库里同名文件的旧分块和旧记录
//  5. 文档存入 DocCollection，文件记录存入 FileCollection
func (p *Pipeline) IngestFile(ctx context.Context, f *multipart.FileHeader, t Target) error {
	filename := silo.UploadFileName(f.Filename)
	if filename == "" {
		return fmt.Errorf("文件名不能为空")
	}
	src, err := f.Open()
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer src.Close()

	filetype, err := silo.GetFileType(src)
	if err != nil {
		return fmt.Errorf("文件类型解析失败: %w", err)
	}
	recordType := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	if recordType == "" {
		recordType = filetype
	}

	if err := p.cfg.Silo.UploadFile([]*multipart.FileHeader{f}, t.Username, t.StoragePrefix()); err != nil {
		return fmt.Errorf("上传对象存储失败: %w", err)
	}

	var (
		docs    []*schema.Document
		content string
	)
	if strings.HasPrefix(filetype, "image/") {
		docs, err = p.imageDocs(ctx, filename, src)
		if err != nil {
			return err
		}
	} else {
		docs, err = p.textDocs(ctx, filename, src)
		if err != nil {
			return err
		}
		var builder strings.Builder
		for _, doc := range docs {
			builder.WriteString(doc.Content)
		}
		content = builder.String()
	}

	// 同名文件重新上传视为覆盖：先删旧分块和旧文件记录，再写新版本。
	if p.cfg.Cleaner != nil {
		if err := p.cfg.Cleaner.DeleteFileVectors(ctx, t.DocCollection, t.FileCollection, filename); err != nil {
			return fmt.Errorf("清理同名旧文件失败: %w", err)
		}
	}

	if len(docs) > 0 {
		if _, err := p.cfg.Indexer.Store(ctx, docs, indexer.WithCollectionName(t.DocCollection)); err != nil {
			return fmt.Errorf("文件存入向量数据库失败: %w", err)
		}
	}
	if err := p.storeFileRecord(ctx, filename, t, content, recordType); err != nil {
		return fmt.Errorf("写入文件记录失败: %w", err)
	}
	return nil
}

// storeFileRecord 把文件的元信息单独写一条记录，供 list_file 之类的工具按文件名找文件。
func (p *Pipeline) storeFileRecord(ctx context.Context, filename string, t Target, content, filetype string) error {
	var summary string
	if content != "" {
		parserPrompt := `你是一个生成文件内容摘要的专家，请你根据用户上传文件的内容，生成一份具有内容细节的摘要。`
		messages := []*schema.Message{schema.SystemMessage(parserPrompt), schema.UserMessage(content)}
		result, err := p.cfg.ChatModel.Generate(ctx, messages)
		if err != nil {
			return err
		}
		summary = result.Content
	}
	vector, err := p.cfg.TextEmbedder.EmbedStrings(ctx, []string{fmt.Sprintf("文件名%s,摘要:%s", filename, summary)})
	if err != nil {
		return err
	}
	doc := &schema.Document{
		ID: uuid.New().String(),
		MetaData: map[string]any{
			"filename":    filename,
			"key":         silo.ObjectKey(t.StoragePrefix(), filename),
			"filetype":    filetype,
			"bucket":      t.Username,
			"kb_id":       strconv.FormatUint(uint64(t.KnowledgeBaseID), 10),
			"summary":     summary,
			"uploaded_at": time.Now().Format(time.RFC3339),
			"vector":      toFloat32(vector[0]),
		},
	}
	_, err = p.cfg.Indexer.Store(ctx, []*schema.Document{doc},
		indexer.WithCollectionName(t.FileCollection))
	return err
}

// imageDocs 用多模态嵌入模型给图片生成向量。
func (p *Pipeline) imageDocs(ctx context.Context, filename string, src io.Reader) ([]*schema.Document, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("读取图片失败: %w", err)
	}
	imagebase64 := base64.StdEncoding.EncodeToString(data)
	vector, err := p.cfg.MultiEmbedder.EmbedStrings(ctx, []string{imagebase64})
	if err != nil {
		return nil, fmt.Errorf("图片向量化失败: %w", err)
	}
	return []*schema.Document{{
		ID:      uuid.New().String(),
		Content: imagebase64,
		MetaData: map[string]any{
			"filename": filename,
			"vector":   toFloat32(vector[0]),
		},
	}}, nil
}

// textDocs 解析并按扩展名切分，再逐块做文本嵌入。
// 每个分块都会带上 filename，供回答时标注来源。
func (p *Pipeline) textDocs(ctx context.Context, filename string, src multipart.File) (docs []*schema.Document, err error) {
	defer func() {
		if r := recover(); r != nil {
			docs = nil
			err = fmt.Errorf("解析文档时发生异常：%v", r)
		}
	}()
	docs, err = p.cfg.Parser.Parse(ctx, src,
		parser.WithFileName(filename),
		einoparser.WithURI(filename),
	)
	if err != nil {
		return nil, fmt.Errorf("文件解析失败: %w", err)
	}

	docs, err = p.cfg.Transformer.Transform(ctx, docs, transformer.WithExtName(filepath.Ext(filename)))
	if err != nil {
		return nil, fmt.Errorf("文件转换失败: %w", err)
	}

	// filename 在这里统一补，而不是靠解析器或切分器保留：
	// 部分切分器（如 PDF）会重建 MetaData，上游塞的键会被丢掉。
	for i, doc := range docs {
		vector, err := p.cfg.TextEmbedder.EmbedStrings(ctx, []string{doc.Content})
		if err != nil {
			return nil, fmt.Errorf("文本向量化失败: %w", err)
		}
		if docs[i].MetaData == nil {
			docs[i].MetaData = make(map[string]any, 2)
		}
		if docs[i].ID == "" {
			docs[i].ID = uuid.New().String()
		}
		if _, ok := docs[i].MetaData["content"]; !ok && doc.Content != "" {
			docs[i].MetaData["content"] = doc.Content
		}
		docs[i].MetaData["filename"] = filename
		docs[i].MetaData["vector"] = toFloat32(vector[0])
	}
	return docs, nil
}

func toFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(f)
	}
	return out
}

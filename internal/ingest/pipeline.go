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
	"strings"

	"rag/internal/indexer"
	"rag/internal/parser"
	"rag/internal/silo"
	"rag/internal/transformer"

	einoparser "github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// fileCollectionSuffix 是文件记录集合的后缀：
// 文档分块进 {username}，文件记录进 {username}file。
const fileCollectionSuffix = "file"

type Config struct {
	// Parser 由 internal/parser.NewExtParser() 构造，类型是 eino 的 ExtParser
	Parser        *einoparser.ExtParser
	Transformer   *transformer.Transformer
	Indexer       *indexer.QdrantIndexer
	Silo          *silo.Silo
	TextEmbedder  embedding.Embedder
	MultiEmbedder embedding.Embedder
}

type Pipeline struct {
	cfg Config
}

func New(cfg *Config) *Pipeline {
	return &Pipeline{cfg: *cfg}
}

// Target 是每次调用携带的用户上下文：集合名与存储桶都用 Username。
type Target struct {
	Username string
	Prefix   string
}

// IngestFile 走完整条流水线：
//  1. 打开文件并嗅探 MIME 类型
//  2. 上传对象存储（bucket = Username，key = Prefix/Filename）
//  3. 写一条文件记录到 {Username}file
//  4. 图片走多模态嵌入；其余走 解析 → 切分 → 逐块文本嵌入
//  5. 存入 {Username}
func (p *Pipeline) IngestFile(ctx context.Context, f *multipart.FileHeader, t Target) error {
	src, err := f.Open()
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer src.Close()

	filetype, err := silo.GetFileType(src)
	if err != nil {
		return fmt.Errorf("文件类型解析失败: %w", err)
	}

	if err := p.cfg.Silo.UploadFile([]*multipart.FileHeader{f}, t.Username, t.Prefix); err != nil {
		return fmt.Errorf("上传对象存储失败: %w", err)
	}

	if err := p.storeFileRecord(ctx, f, t); err != nil {
		return fmt.Errorf("写入文件记录失败: %w", err)
	}

	var docs []*schema.Document
	if strings.HasPrefix(filetype, "image/") {
		docs, err = p.imageDocs(ctx, f.Filename, src)
	} else {
		docs, err = p.textDocs(ctx, f, src)
	}
	if err != nil {
		return err
	}

	if _, err := p.cfg.Indexer.Store(ctx, docs, indexer.WithCollectionName(t.Username)); err != nil {
		return fmt.Errorf("文件存入向量数据库失败: %w", err)
	}
	return nil
}

// storeFileRecord 把文件的元信息单独写一条记录，供 list_file 之类的工具按文件名找文件。
func (p *Pipeline) storeFileRecord(ctx context.Context, f *multipart.FileHeader, t Target) error {
	var filetype string
	if v := f.Header["filetype"]; len(v) > 0 {
		filetype = v[0]
	}
	vector, err := p.cfg.TextEmbedder.EmbedStrings(ctx, []string{f.Filename})
	if err != nil {
		return err
	}
	doc := &schema.Document{
		ID: uuid.New().String(),
		MetaData: map[string]any{
			"filename": f.Filename,
			"filetype": filetype,
			"bucket":   t.Username,
			"vector":   toFloat32(vector[0]),
		},
	}
	_, err = p.cfg.Indexer.Store(ctx, []*schema.Document{doc},
		indexer.WithCollectionName(t.Username+fileCollectionSuffix))
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
func (p *Pipeline) textDocs(ctx context.Context, f *multipart.FileHeader, src multipart.File) ([]*schema.Document, error) {
	docs, err := p.cfg.Parser.Parse(ctx, src,
		parser.WithFileName(f.Filename),
		einoparser.WithURI(f.Filename),
	)
	if err != nil {
		return nil, fmt.Errorf("文件解析失败: %w", err)
	}

	docs, err = p.cfg.Transformer.Transform(ctx, docs, transformer.WithExtName(filepath.Ext(f.Filename)))
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
		docs[i].MetaData["filename"] = f.Filename
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

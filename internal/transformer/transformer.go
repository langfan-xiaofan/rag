package transformer

import (
	"context"
	"unicode/utf8"

	"github.com/cloudwego/eino-ext/components/document/transformer/splitter/html"
	"github.com/cloudwego/eino-ext/components/document/transformer/splitter/markdown"
	"github.com/cloudwego/eino-ext/components/document/transformer/splitter/recursive"
	"github.com/cloudwego/eino-ext/components/document/transformer/splitter/semantic"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
)

type Transformer struct {
	MD        document.Transformer
	Html      document.Transformer
	Recursive document.Transformer
	Semantic  document.Transformer
	Pdf       document.Transformer
	Ext       string
}

func NewTransformer(chunkSize int, overLap int, embedder embedding.Embedder) *Transformer {
	ctx := context.Background()
	md, _ := markdown.NewHeaderSplitter(ctx, &markdown.HeaderConfig{})
	htmlSplitter, _ := html.NewHeaderSplitter(ctx, &html.HeaderConfig{})
	re, _ := recursive.NewSplitter(ctx, &recursive.Config{
		ChunkSize:   chunkSize,
		OverlapSize: overLap,
		LenFunc:     Len,
		Separators:  []string{"\n\n", "\n", "？", "！", "。", "；", "，"},
	})
	pdf := NewPDFTransformer(chunkSize, overLap)
	sema, _ := semantic.NewSplitter(ctx, &semantic.Config{
		Embedding:    embedder,
		BufferSize:   1,
		MinChunkSize: 50,
		Percentile:   0.9,
	})
	return &Transformer{
		MD:        md,
		Html:      htmlSplitter,
		Recursive: re,
		Pdf:       pdf,
		Semantic:  sema,
	}
}

func Len(s string) int {
	return utf8.RuneCountInString(s)
}

func (t *Transformer) Transform(ctx context.Context, src []*schema.Document, opts ...document.TransformerOption) ([]*schema.Document, error) {
	document.GetTransformerImplSpecificOptions(t, opts...)
	switch t.Ext {
	case ".md":
		return t.MD.Transform(ctx, src)
	case ".docx":
		return t.Recursive.Transform(ctx, src)
	case ".html":
		return t.Html.Transform(ctx, src)
	case ".pdf":
		return t.Pdf.Transform(ctx, src)
	default:
		return t.Semantic.Transform(ctx, src)
	}
}

func WithExtName(ext string) document.TransformerOption {
	return document.WrapTransformerImplSpecificOptFn(func(t *Transformer) {
		t.Ext = ext
	})
}

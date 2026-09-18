package parser

import (
	"context"

	"github.com/cloudwego/eino-ext/components/document/parser/docx"
	"github.com/cloudwego/eino-ext/components/document/parser/html"
	"github.com/cloudwego/eino/components/document/parser"
)

type Parser struct {
}

func NewExtParser() (*parser.ExtParser, error) {
	ctx := context.Background()

	textParser := parser.TextParser{}

	htmlParser, _ := html.NewParser(ctx, &html.Config{
		Selector: new("body"),
	})

	docsParser, err := docx.NewDocxParser(ctx, &docx.Config{
		ToSections: true,
	})
	if err != nil {
		return nil, err
	}

	pdfParser := NewPDFParser(500, 200)

	// 创建扩展解析器
	extParser, err := parser.NewExtParser(ctx, &parser.ExtParserConfig{
		// 注册特定扩展名的解析器
		Parsers: map[string]parser.Parser{
			".html": htmlParser,
			".pdf":  pdfParser,
			".docx": docsParser,
		},
		// 设置默认解析器，用于处理未知格式
		FallbackParser: textParser,
	})
	if err != nil {
		return nil, err
	}
	return extParser, nil

}

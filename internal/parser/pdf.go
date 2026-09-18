package parser

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/schema"
	"github.com/gen2brain/go-fitz"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
)

// PDFParser 实现 eino 的 parser.Parser 与 document.Loader 接口。
// 主路径用 ledongthuc/pdf 按行距切分；文本层缺失（扫描件）时整份降级给 go-fitz。
type PDFParser struct {
	fileName string
}

func NewPDFParser() *PDFParser {
	return &PDFParser{}
}

// ParsePDFByFitz 用 go-fitz（MuPDF）并发提取每页文本，作为扫描件的降级方案。
//
// go-fitz 的单个 Document 内部有互斥锁、无法并发，这里按 worker 数打开多个
// Document 实例（各自独立的 MuPDF context），按页分片并行提取。
//
// 入参是整份文件的字节而不是 io.Reader：fitz.NewFromReader 内部就是 io.ReadAll，
// 会把 reader 读到 EOF，而我们要从同一份数据里开多个实例。
func ParsePDFByFitz(data []byte) ([]*schema.Document, error) {
	first, err := fitz.NewFromMemory(data)
	if err != nil {
		return nil, fmt.Errorf("打开 PDF 失败: %w", err)
	}
	numPage := first.NumPage()
	source := first.Metadata()["title"]

	workers := min(runtime.NumCPU(), 8, numPage) // 每个实例有独立 MuPDF store，过多会成倍占内存
	if workers < 1 {
		workers = 1
	}

	docs := make([]*fitz.Document, workers)
	docs[0] = first
	defer func() {
		for _, d := range docs {
			if d != nil {
				d.Close()
			}
		}
	}()
	for i := 1; i < workers; i++ {
		d, err := fitz.NewFromMemory(data)
		if err != nil {
			return nil, fmt.Errorf("打开 PDF 失败（第 %d 个实例）: %w", i, err)
		}
		docs[i] = d
	}

	// 先把每个下标都初始化好。worker 按下标各写各的，留空指针会在赋值时 panic，
	// 而 panic 发生在子 goroutine 里，调用方的 recover 接不住，会直接终止进程。
	pages := make([]*schema.Document, numPage)
	for i := range pages {
		pages[i] = &schema.Document{
			ID: uuid.New().String(),
			MetaData: map[string]any{
				"page":   i,
				"source": source,
			},
		}
	}

	var mu sync.Mutex
	var firstErr error

	ch := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(d *fitz.Document) {
			defer wg.Done()
			for i := range ch {
				text, err := d.Text(i)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("读取第%d页失败: %w", i, err)
					}
					mu.Unlock()
					continue
				}
				pages[i].Content = strings.ReplaceAll(text, "\n", "")
				pages[i].MetaData["content"] = pages[i].Content
			}
		}(docs[w])
	}

	for i := 0; i < numPage; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return pages, nil
}

// Parse 用 ledongthuc/pdf 提取每页文本，按行距与句末标点切成段落。
func (p *PDFParser) Parse(ctx context.Context, reader io.Reader, opts ...parser.Option) ([]*schema.Document, error) {
	// apply 是未导出字段，实现自定义选项必须通过 parser.GetImplSpecificOptions 应用
	parser.GetImplSpecificOptions(p, opts...)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取 PDF 失败: %w", err)
	}

	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("解析 PDF 失败: %w", err)
	}

	var texts []*schema.Document
	numPage := r.NumPage()
	for i := 1; i <= numPage; i++ {
		items := r.Page(i).Content().Text

		// 这一页没有任何文本元素、且此前一无所获：整份文档大概率是扫描件。
		// 直接返回 fitz 的结果，不再混用两种提取方式——ParsePDFByFitz 返回的是
		// 整份文档，继续往下走会把后面的页重复提取一遍。
		if len(items) == 0 && len(texts) == 0 {
			return ParsePDFByFitz(data)
		}

		var buff strings.Builder
		lastY := 0.0 // 每页独立，跨页比较 Y 没有意义
		for _, t := range items {
			if math.Abs(t.Y-lastY) > 0.5*t.FontSize && strings.HasSuffix(buff.String(), "。") {
				texts = append(texts, p.newDocument(buff.String(), i))
				buff.Reset()
			}
			lastY = t.Y
			// 去掉页脚的信息
			if t.Y > 20 {
				buff.WriteString(t.S)
			}
		}
		if buff.Len() != 0 {
			texts = append(texts, p.newDocument(buff.String(), i))
		}
	}
	return texts, nil
}

// newDocument 构造一个分块。content 同时写进 Content 和 MetaData["content"]：
// 前者用于向量化，后者会被 indexer 写进 payload、供检索时还原成文档正文。
func (p *PDFParser) newDocument(content string, page int) *schema.Document {
	return &schema.Document{
		Content: content,
		ID:      uuid.NewString(),
		MetaData: map[string]any{
			"content": content,
			"page":    page,
			"source":  p.fileName,
		},
	}
}

func WithFileName(filename string) parser.Option {
	return parser.WrapImplSpecificOptFn(func(o *PDFParser) {
		o.fileName = filename
	})
}

// Load 实现 eino 的 document.Loader，从本地路径读取 PDF。
func (p *PDFParser) Load(ctx context.Context, src document.Source, opts ...document.LoaderOption) ([]*schema.Document, error) {
	f, _, err := pdf.Open(src.URI)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return p.Parse(ctx, f, WithFileName(f.Name()))
}

var _ document.Loader = (*PDFParser)(nil)

var _ parser.Parser = (*PDFParser)(nil)

package parser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"

	einopdf "github.com/cloudwego/eino-ext/components/document/parser/pdf"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/schema"
	"github.com/gen2brain/go-fitz"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
)

type PDFParser struct {
	fileName  string
	ChunkSize int
	Overlap   int
}

type Document struct {
	ID       string
	Content  string
	MetaData map[string]any
}

func NewPDFParser(chunksize, overlap int) *PDFParser {
	return &PDFParser{
		ChunkSize: chunksize,
		Overlap:   overlap,
	}
}

// Parse 并发提取 PDF 每页文本。
// go-fitz 的单个 Document 内部有互斥锁，无法并发；这里按 worker 数，默认是8个worker
// 打开多个 Document 实例（各自独立的 MuPDF context），按页分片并行提取。
func ParsePDFByFitz(reader io.Reader) ([]*schema.Document, error) {
	// 先开一个实例获取页数，再决定实际需要的 worker 数
	first, err := fitz.NewFromReader(reader)
	if err != nil {
		return nil, errors.New("打开文件失败1" + err.Error())
	}
	numPage := first.NumPage()

	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8 // 每个实例有独立 MuPDF store，过多会成倍占内存
	}
	if workers > numPage {
		workers = numPage
	}
	if workers < 1 {
		workers = 1
	}

	docs := make([]*fitz.Document, workers)
	docs[0] = first
	for i := 1; i < workers; i++ {
		d, err := fitz.NewFromReader(reader)
		if err != nil {
			for _, opened := range docs[:i] {
				opened.Close()
			}
			return nil, errors.New("打开文件失败2")
		}
		docs[i] = d
	}
	for _, d := range docs {
		defer d.Close()
	}

	pages := make([]*schema.Document, numPage)
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
						firstErr = fmt.Errorf("读取第%d页失败: %v", i, err)
					}
					mu.Unlock()
					continue
				}
				pages[i].Content = text // 各写各的下标，无需加锁
				pages[i].ID = uuid.New().String()
				pages[i].MetaData["page"] = i
				pages[i].MetaData["content"] = text
				pages[i].MetaData["source"] = first.Metadata()["title"]
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
	for k := range pages {
		pages[k].Content = strings.ReplaceAll(pages[k].Content, "\n", "")
	}
	return pages, nil
}

// Parse 将PDF的每一行的文字按照切片返回
func (p *PDFParser) Parse(ctx context.Context, reader io.Reader, opts ...parser.Option) ([]*schema.Document, error) {
	// apply 是未导出字段，实现自定义选项必须通过 parser.GetImplSpecificOptions 应用
	parser.GetImplSpecificOptions(p, opts...)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("pdf parser read all from reader failed: %w", err)
	}

	readerAt := bytes.NewReader(data)
	r, err := pdf.NewReader(readerAt, int64(readerAt.Len()))
	if err != nil {
		return nil, err
	}
	var texts []*schema.Document
	lastY := 0.0
	fmt.Println(r.NumPage())
	for i := 1; i <= r.NumPage(); i++ {
		var buff strings.Builder
		for _, t := range r.Page(i).Content().Text {
			if math.Abs(t.Y-lastY) > 0.5*t.FontSize && strings.HasSuffix(buff.String(), "。") { // 假设行间距大于1.0时认为是新行
				texts = append(texts, &schema.Document{
					Content: buff.String(),
					ID:      uuid.NewString(),
					MetaData: map[string]any{
						"content": buff.String(),
						"page":    i,
						"source":  p.fileName,
					},
				})
				buff.Reset()
			}
			lastY = t.Y
			//去掉页脚的信息
			if t.Y > 20 {
				buff.Write([]byte(t.S))
			}
			// time.Sleep(time.Millisecond * 10) // 避免输出过快，导致终端卡顿
		}
		if buff.Len() != 0 {
			texts = append(texts, &schema.Document{
				Content: buff.String(),
				ID:      uuid.NewString(),
				MetaData: map[string]any{
					"content": buff.String(),
					"page":    i,
					"source":  p.fileName,
				},
			})
		}
		// fmt.Printf("texts:%v", texts)
		//如果该页面没有任何的文本，就降级使用fitz来分析
		if len(r.Page(i).Content().Text) == 0 && len(texts) == 0 {
			Newtexts, err := ParsePDFByFitz(bytes.NewReader(data))
			if err != nil {
				log.Fatal(err)
				return nil, err
			}
			fmt.Println(Newtexts)
			texts = append(texts, Newtexts...)
		}
	}
	return texts, nil
}

func WithFileName(filename string) parser.Option {
	return parser.WrapImplSpecificOptFn(func(o *PDFParser) {

		o.fileName = filename
	})
}

// Load 该方法是用于嵌入Eino框架的实现，用于返回schema的Document切片

func (p *PDFParser) Load(ctx context.Context, src document.Source, opts ...document.LoaderOption) ([]*schema.Document, error) {
	f, _, err := pdf.Open(src.URI)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// var docs []*schema.Document
	// var buf strings.Builder
	return p.Parse(ctx, f, WithFileName(f.Name()))
	// for _, text := range texts {
	// 	doc := &schema.Document{
	// 		ID:      text.ID,
	// 		Content: text.Content,
	// 		MetaData: map[string]any{
	// 			"filename": f.Name(),
	// 		},
	// 	}
	// 	docs = append(docs, doc)
	// }
	// for i := range r.NumPage() {
	// 	for _, t := range r.Page(i).Content().Text {
	// 		if t.Y >= 20 {
	// 			buf.WriteString(t.S)
	// 		}
	// 	}
	// 	doc := &schema.Document{
	// 		Content: buf.String(),
	// 		MetaData: map[string]interface{}{
	// 			"page": i + 1,
	// 		},
	// 	}
	// 	buf.Reset()
	// 	docs = append(docs, doc)
	// }
	// return docs, nil
}

func ParsePDFByEino(filePath string) ([]*schema.Document, error) {
	ctx := context.Background()

	parser, err := einopdf.NewPDFParser(ctx, &einopdf.Config{
		ToPages: true,
	})
	if err != nil {
		log.Fatalf("pdf.NewPDFParser failed, err=%v", err)
	}

	file, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("os.Open failed, err=%v", err)
	}
	defer file.Close()

	docs, err := parser.Parse(ctx, file)
	if err != nil {
		log.Fatalf("parser.Parse failed, err=%v", err)
	}

	// log.Printf("解析了 %d 个文档", len(docs))
	log.Printf("内容: %s", docs[0].Content)
	return docs, nil
}

var _ document.Loader = (*PDFParser)(nil)

var _ parser.Parser = (*PDFParser)(nil)

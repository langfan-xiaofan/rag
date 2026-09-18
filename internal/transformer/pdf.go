package transformer

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
)

type PDF struct {
	ChunkSize int
	Overlap   int
}

func NewPDFTransformer(chunksize, overlap int) *PDF {
	return &PDF{
		ChunkSize: chunksize,
		Overlap:   overlap,
	}
}

func (p *PDF) Transform(ctx context.Context, src []*schema.Document, opts ...document.TransformerOption) ([]*schema.Document, error) {
	page := 1
	docs := []*schema.Document{}
	total := 0
	var buff strings.Builder
	goodList := []string{}
	for _, doc := range src {
		if doc.MetaData["page"].(int) != page {
			docs = append(docs, &schema.Document{
				ID:      doc.ID,
				Content: buff.String(),
				MetaData: map[string]any{
					"source":  doc.MetaData["source"],
					"page":    doc.MetaData["page"],
					"content": buff.String(),
				},
			})
			buff.Reset()
			page = doc.MetaData["page"].(int)
			goodList = []string{}
			total = 0
		}
		if total+utf8.RuneCountInString(doc.Content) < p.ChunkSize {
			goodList = append(goodList, doc.Content)
			total += utf8.RuneCountInString(doc.Content)
			buff.WriteString(doc.Content)
		} else {
			docs = append(docs, &schema.Document{
				ID:      doc.ID,
				Content: buff.String(),
				MetaData: map[string]any{
					"source":  doc.MetaData["source"],
					"page":    doc.MetaData["page"],
					"content": buff.String(),
				},
			})
			for total > p.Overlap {
				total -= utf8.RuneCountInString(goodList[0])
				goodList = goodList[1:]
			}
			goodList = append(goodList, doc.Content)
			buff.Reset()
			buff.WriteString(strings.Join(goodList, ""))
		}
	}
	if buff.Len() != 0 {
		docs = append(docs, &schema.Document{
			ID:      src[len(src)-1].ID,
			Content: buff.String(),
			MetaData: map[string]any{
				"source":  src[len(src)-1].MetaData["source"],
				"page":    src[len(src)-1].MetaData["page"],
				"content": buff.String(),
			},
		})
	}
	return docs, nil
}

package retriever

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"rag/utils/bm25"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/qdrant/go-client/qdrant"
)

type Config struct {
	Client     *qdrant.Client
	Collection string
	TopK       int
	Embedder   embedding.Embedder
}

type QdrantRetriever struct {
	client     *qdrant.Client
	collection string
	topK       int
	embedder   embedding.Embedder
}

func NewQdrantRetriever(config Config) *QdrantRetriever {
	return &QdrantRetriever{
		client:     config.Client,
		collection: config.Collection,
		topK:       config.TopK,
		embedder:   config.Embedder,
	}
}

func WithCollectionName(collection string) retriever.Option {
	return retriever.WrapImplSpecificOptFn(func(t *QdrantRetriever) {
		t.collection = collection
	})
}

// Retrieve 从Qdrant里面检索出需要的文档
func (r *QdrantRetriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) (docs []*schema.Document, err error) {
	local := *r
	options := retriever.GetCommonOptions(&retriever.Options{
		TopK: new(local.topK),
	}, opts...)
	retriever.GetImplSpecificOptions(&local, opts...)
	if local.embedder == nil {
		return nil, errors.New("embedder is nil")
	}
	queryVector, err := local.embedder.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	ctx = callbacks.EnsureRunInfo(ctx, local.GetType(), components.ComponentOfRetriever)
	ctx = callbacks.OnStart(ctx, &retriever.CallbackInput{
		Query: query,
		TopK:  local.topK,
		Extra: map[string]any{
			"collection": local.collection,
		},
	})
	vec32 := make([]float32, len(queryVector[0]))
	for i, v := range queryVector[0] {
		vec32[i] = float32(v)
	}
	searchResult, err := local.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: local.collection,
		Query:          qdrant.NewQuery(vec32...),
		Limit:          new(uint64(max(*options.TopK, 5))),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}
	docs = make([]*schema.Document, 0, len(searchResult))
	for _, result := range searchResult {
		doc := &schema.Document{
			ID:       result.Id.GetUuid(),
			Content:  result.Payload["content"].GetStringValue(),
			MetaData: map[string]any{},
		}
		//if val, ok := result.Payload["content"]; ok {
		//	doc.Content = val.GetStringValue()
		//}
		for k, v := range result.Payload {
			if k == "content" {
				continue
			}
			doc.MetaData[k] = v.GetStringValue()
		}
		doc.WithScore(float64(result.Score))
		docs = append(docs, doc)
	}
	callbacks.OnEnd(ctx, &retriever.CallbackOutput{
		Docs: docs,
	})
	docs = bm25.BM25(docs, query)
	slices.SortFunc(docs, func(a, b *schema.Document) int {
		return cmp.Compare(b.Score(), a.Score())
	})
	return docs, nil
}

func (r *QdrantRetriever) GetType() string {
	return "qdrant"
}

var _ retriever.Retriever = (*QdrantRetriever)(nil)

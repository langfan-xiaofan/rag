package indexer

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/qdrant/go-client/qdrant"
)

type Config struct {
	Client     *qdrant.Client
	Collection string
	Dimension  int
}

// QdrantIndexer 用于文档的向量化与存储
type QdrantIndexer struct {
	client     *qdrant.Client
	collection string
	dimension  int
}

func NewQdrantIndexer(config *Config, opts ...indexer.Option) *QdrantIndexer {
	idx := &QdrantIndexer{
		client:     config.Client,
		collection: config.Collection,
		dimension:  config.Dimension,
	}
	indexer.GetImplSpecificOptions(idx, opts...)
	return idx
}

// WithCollectionName 设置存放向量的集合
func WithCollectionName(collection string) indexer.Option {
	return indexer.WrapImplSpecificOptFn(func(q *QdrantIndexer) {
		q.collection = collection
	})
}

func (q *QdrantIndexer) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) (ids []string, err error) {
	local := *q
	indexer.GetImplSpecificOptions(&local, opts...)
	exist, err := local.client.CollectionExists(ctx, local.collection)
	if err != nil {
		return nil, errors.New("检查集合是否存在发生错误 " + err.Error())
	}
	if !exist {
		if err = local.client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: local.collection,
			VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
				Size:     uint64(local.dimension),
				Distance: qdrant.Distance_Cosine,
			}),
		}); err != nil {
			return nil, errors.New("创建集合失败 " + err.Error())
		}
	}
	points := make([]*qdrant.PointStruct, 0, len(docs))
	ids = make([]string, 0, len(docs))
	for _, doc := range docs {
		vector, ok := doc.MetaData["vector"].([]float32)
		if !ok {
			return nil, fmt.Errorf("文档 %s 的 MetaData[\"vector\"] 缺失或不是 []float32", doc.ID)
		}
		// vector 只用于生成点，不属于 payload
		payload := make(map[string]any, len(doc.MetaData))
		for k, v := range doc.MetaData {
			if k == "vector" {
				continue
			}
			payload[k] = v
		}
		ids = append(ids, doc.ID)
		points = append(points, &qdrant.PointStruct{
			Id:      qdrant.NewID(doc.ID),
			Vectors: qdrant.NewVectorsDense(vector),
			Payload: qdrant.NewValueMap(payload),
		})
	}
	_, err = local.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: local.collection,
		Points:         points,
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (q *QdrantIndexer) GetType() string {
	return "qdrant"
}

var _ indexer.Indexer = (*QdrantIndexer)(nil)

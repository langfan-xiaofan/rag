package embedder

import (
	"context"
	"errors"
	"rag/internal/config"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino/components/embedding"
)

func NewTextEmbedder(ctx context.Context) (embedding.Embedder, error) {
	embedder, err := openai.NewEmbedder(ctx, &openai.EmbeddingConfig{
		BaseURL: config.Conf.Embedder.BaseUrl,
		Model:   config.Conf.Embedder.Model,
		APIKey:  config.Conf.Embedder.ApiKey,
	})
	if err != nil {
		return nil, errors.New("初始化文本嵌入模型失败")
	}
	return embedder, nil
}

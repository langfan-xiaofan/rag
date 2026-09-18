package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/cloudwego/eino/components/embedding"
)

type Config struct {
	BaseUrl string
	ApiKey  string
	Model   string
}

type MultimodalEmbedder struct {
	baseUrl string
	apiKey  string
	model   string
}

func NewMultimodalEmbedder(config Config) embedding.Embedder {
	return &MultimodalEmbedder{
		baseUrl: config.BaseUrl,
		apiKey:  config.ApiKey,
		model:   config.Model,
	}
}

func (e *MultimodalEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {

	client := &http.Client{Timeout: 2 * time.Minute}
	marker, err := mediaMarker(client, e.baseUrl)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	vectors := make([][]float64, 0)
	for _, text := range texts {
		// 每张图片各发一次请求，才能拿到各自的 embedding
		vec, _, err := embedImage(client, marker, text, e.baseUrl)
		if err != nil {
			return nil, err
		}
		vectors = append(vectors, vec)
	}
	return vectors, nil
}

type serverProps struct {
	MediaMarker string `json:"media_marker"`
}

type embeddingsInput struct {
	PromptString   string `json:"prompt_string"`
	MultimodalData string `json:"multimodal_data"`
}

type embeddingsRequest struct {
	Input embeddingsInput `json:"input"`
}

type embeddingsResponse struct {
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// embedImage 把图片进行向量化返回它的embedding。
// prompt_string 里 marker 的数量必须与 paths 数量一致，且按 data 的顺序对应。
func embedImage(client *http.Client, marker string, imagesbase64 string, BaseURL string) ([]float64, int, error) {
	body, err := json.Marshal(embeddingsRequest{
		Input: embeddingsInput{
			PromptString:   marker,
			MultimodalData: imagesbase64,
		},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, BaseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("call embeddings: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("embeddings: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}

	var out embeddingsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, 0, fmt.Errorf("decode response: %w", err)
	}
	if len(out.Data) == 0 {
		return nil, 0, fmt.Errorf("embeddings: response has no data")
	}
	return out.Data[0].Embedding, out.Usage.PromptTokens, nil
}

func mediaMarker(client *http.Client, BaseUrl string) (string, error) {
	resp, err := client.Get(BaseUrl + "/props")
	if err != nil {
		return "", fmt.Errorf("get /props: %w", err)
	}
	defer resp.Body.Close()

	var props serverProps
	if err := json.NewDecoder(resp.Body).Decode(&props); err != nil {
		return "", fmt.Errorf("decode /props: %w", err)
	}
	if props.MediaMarker == "" {
		return "", fmt.Errorf("/props returned no media_marker; server does not support multimodal")
	}
	return props.MediaMarker, nil
}

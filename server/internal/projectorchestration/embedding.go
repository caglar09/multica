package projectorchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMemoryEmbeddingModel      = "text-embedding-3-small"
	defaultMemoryEmbeddingDimensions = 1536
	defaultMemoryEmbeddingBaseURL    = "https://api.openai.com/v1"
	maxMemoryEmbeddingTextRunes      = 6000
)

// MemoryEmbedder converts private project memory into a vector. Production
// callers use the opt-in OpenAI adapter; tests can inject a deterministic fake.
type MemoryEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// MemoryEmbeddingConfig configures opt-in project brain vector generation.
type MemoryEmbeddingConfig struct {
	Enabled bool
	APIKey  string
	BaseURL string
}

type openAIEmbedder struct {
	apiKey string
	url    string
	model  string
	client *http.Client
}

func memoryEmbeddingConfigFromEnvironment() MemoryEmbeddingConfig {
	// The dedicated switch and key make project-text export an explicit server opt-in.
	return MemoryEmbeddingConfig{
		Enabled: strings.EqualFold(strings.TrimSpace(os.Getenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED")), "true"),
		APIKey:  strings.TrimSpace(os.Getenv("MULTICA_PROJECT_BRAIN_EMBEDDING_API_KEY")),
		BaseURL: strings.TrimSpace(os.Getenv("MULTICA_PROJECT_BRAIN_EMBEDDING_BASE_URL")),
	}
}

func memoryEmbedderFromEnvironment() (MemoryEmbedder, error) {
	return memoryEmbedderForConfig(memoryEmbeddingConfigFromEnvironment(), nil)
}

// PrepareBrainMemoryEmbedding prepares one entry vector before its caller opens
// a database transaction. An empty literal means embedding is explicitly off.
func PrepareBrainMemoryEmbedding(ctx context.Context, subject string, content []byte) (string, error) {
	embedder, err := memoryEmbedderFromEnvironment()
	if err != nil {
		return "", err
	}
	return embedMemoryText(ctx, embedder, memoryEmbeddingText(subject, content))
}

func memoryEmbedderForConfig(config MemoryEmbeddingConfig, injected MemoryEmbedder) (MemoryEmbedder, error) {
	if !config.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("project brain embeddings are enabled without a server API key")
	}
	if injected != nil {
		return injected, nil
	}
	return newMemoryEmbedder(config, nil)
}

func newMemoryEmbedder(config MemoryEmbeddingConfig, client *http.Client) (MemoryEmbedder, error) {
	if !config.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("project brain embeddings are enabled without a server API key")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultMemoryEmbeddingBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("invalid project brain embedding base URL")
	}
	if parsed.Scheme == "http" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return nil, errors.New("project brain embedding base URL must use HTTPS")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &openAIEmbedder{
		apiKey: strings.TrimSpace(config.APIKey),
		url:    baseURL + "/embeddings",
		model:  defaultMemoryEmbeddingModel,
		client: client,
	}, nil
}

func (e *openAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e == nil || e.client == nil || strings.TrimSpace(e.apiKey) == "" {
		return nil, nil
	}
	text = limitEmbeddingText(text)
	if text == "" {
		return nil, nil
	}
	requestBody, err := json.Marshal(struct {
		Model      string `json:"model"`
		Input      string `json:"input"`
		Dimensions int    `json:"dimensions"`
	}{Model: e.model, Input: text, Dimensions: defaultMemoryEmbeddingDimensions})
	if err != nil {
		return nil, fmt.Errorf("encode project brain embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("create project brain embedding request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request project brain embedding: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read project brain embedding response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("project brain embedding API returned status %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode project brain embedding response: %w", err)
	}
	for _, item := range result.Data {
		if item.Index == 0 {
			if err := validateMemoryEmbedding(item.Embedding); err != nil {
				return nil, err
			}
			return item.Embedding, nil
		}
	}
	return nil, errors.New("project brain embedding response is missing index zero")
}

func limitEmbeddingText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > maxMemoryEmbeddingTextRunes {
		text = string(runes[:maxMemoryEmbeddingTextRunes])
	}
	return strings.TrimSpace(text)
}

func memoryEmbeddingText(subject string, content []byte) string {
	return limitEmbeddingText(strings.TrimSpace(subject) + "\n" + strings.TrimSpace(string(content)))
}

func validateMemoryEmbedding(vector []float32) error {
	if len(vector) != defaultMemoryEmbeddingDimensions {
		return fmt.Errorf("project brain embedding has %d dimensions, want %d", len(vector), defaultMemoryEmbeddingDimensions)
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return errors.New("project brain embedding contains a non-finite value")
		}
	}
	return nil
}

func memoryEmbeddingVectorLiteral(vector []float32) (string, error) {
	if err := validateMemoryEmbedding(vector); err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(len(vector) * 8)
	b.WriteByte('[')
	for i, value := range vector {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(value), 'g', 8, 32))
	}
	b.WriteByte(']')
	return b.String(), nil
}

func embedMemoryText(ctx context.Context, embedder MemoryEmbedder, text string) (string, error) {
	if embedder == nil || strings.TrimSpace(text) == "" {
		return "", nil
	}
	vector, err := embedder.Embed(ctx, limitEmbeddingText(text))
	if err != nil {
		return "", err
	}
	if len(vector) == 0 {
		return "", nil
	}
	return memoryEmbeddingVectorLiteral(vector)
}

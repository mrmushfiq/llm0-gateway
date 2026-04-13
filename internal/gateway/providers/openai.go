package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/mrmushfiq/llm0-gateway/internal/shared/config"
	"github.com/sashabaranov/go-openai"
)

// OpenAIProvider handles OpenAI API requests
type OpenAIProvider struct {
	client *openai.Client
	cfg    *config.Config
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Model       string                         `json:"model"`
	Messages    []openai.ChatCompletionMessage `json:"messages"`
	Temperature *float32                       `json:"temperature,omitempty"`
	MaxTokens   *int                           `json:"max_tokens,omitempty"`
	TopP        *float32                       `json:"top_p,omitempty"`
	Stream      bool                           `json:"stream,omitempty"`
}

// ChatResponse represents a chat completion response
type ChatResponse struct {
	ID                string                        `json:"id"`
	Object            string                        `json:"object"`
	Created           int64                         `json:"created"`
	Model             string                        `json:"model"`
	Choices           []openai.ChatCompletionChoice `json:"choices"`
	Usage             openai.Usage                  `json:"usage"`
	SystemFingerprint string                        `json:"system_fingerprint,omitempty"`

	// Performance metrics
	LatencyMs int     `json:"latency_ms,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(cfg *config.Config) *OpenAIProvider {
	client := openai.NewClient(cfg.OpenAIAPIKey)
	return &OpenAIProvider{
		client: client,
		cfg:    cfg,
	}
}

// NewOpenAIProviderWithKey creates a provider with a custom API key (for BYOK)
func NewOpenAIProviderWithKey(apiKey string) *OpenAIProvider {
	client := openai.NewClient(apiKey)
	return &OpenAIProvider{
		client: client,
	}
}

// ChatCompletion makes a chat completion request to OpenAI
func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	startTime := time.Now()

	// Build OpenAI request
	openaiReq := openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: req.Messages,
	}

	if req.Temperature != nil {
		openaiReq.Temperature = *req.Temperature
	}
	if req.MaxTokens != nil {
		openaiReq.MaxTokens = *req.MaxTokens
	}
	if req.TopP != nil {
		openaiReq.TopP = *req.TopP
	}

	// Make request
	resp, err := p.client.CreateChatCompletion(ctx, openaiReq)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %w", err)
	}

	latencyMs := int(time.Since(startTime).Milliseconds())

	// Build response
	return &ChatResponse{
		ID:                resp.ID,
		Object:            resp.Object,
		Created:           resp.Created,
		Model:             resp.Model,
		Choices:           resp.Choices,
		Usage:             resp.Usage,
		SystemFingerprint: resp.SystemFingerprint,
		LatencyMs:         latencyMs,
	}, nil
}

// ListModels returns available OpenAI models
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]string, error) {
	models, err := p.client.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}

	var modelIDs []string
	for _, model := range models.Models {
		modelIDs = append(modelIDs, model.ID)
	}

	return modelIDs, nil
}

// ChatCompletionStream creates a streaming chat completion request to OpenAI
func (p *OpenAIProvider) ChatCompletionStream(ctx context.Context, req ChatRequest) (*openai.ChatCompletionStream, error) {
	// Build OpenAI request
	openaiReq := openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   true, // Enable streaming
	}

	if req.Temperature != nil {
		openaiReq.Temperature = *req.Temperature
	}
	if req.MaxTokens != nil {
		openaiReq.MaxTokens = *req.MaxTokens
	}
	if req.TopP != nil {
		openaiReq.TopP = *req.TopP
	}

	// Create streaming request
	stream, err := p.client.CreateChatCompletionStream(ctx, openaiReq)
	if err != nil {
		return nil, fmt.Errorf("OpenAI streaming API error: %w", err)
	}

	return stream, nil
}

// ValidateModel checks if a model is valid for chat completions
func (p *OpenAIProvider) ValidateModel(model string) bool {
	validModels := map[string]bool{
		"gpt-4":             true,
		"gpt-4-turbo":       true,
		"gpt-4o":            true,
		"gpt-4o-mini":       true,
		"gpt-3.5-turbo":     true,
		"gpt-3.5-turbo-16k": true,
	}
	return validModels[model]
}

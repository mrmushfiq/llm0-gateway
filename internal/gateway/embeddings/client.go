package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client handles communication with the embedding service
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// embedRequest represents the request body for the embedding service
type embedRequest struct {
	Texts []string `json:"texts"`
}

// embedResponse represents the response from the embedding service
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Model      string      `json:"model"`
	Dimensions int         `json:"dimensions"`
}

// NewClient creates a new embedding client
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GenerateEmbedding generates an embedding for a single text
func (c *Client) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := c.GenerateEmbeddings(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return embeddings[0], nil
}

// GenerateEmbeddings generates embeddings for multiple texts (batch)
func (c *Client) GenerateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}
	if len(texts) > 100 {
		return nil, fmt.Errorf("too many texts (max 100, got %d)", len(texts))
	}

	// Prepare request body
	reqBody := embedRequest{Texts: texts}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/embed", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call embedding service: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding service returned status %d", resp.StatusCode)
	}

	// Parse response
	var embResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Validate response
	if len(embResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: expected %d, got %d", len(texts), len(embResp.Embeddings))
	}

	return embResp.Embeddings, nil
}

// HealthCheck checks if the embedding service is healthy
func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	return nil
}


package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGenerateEmbedding(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("Expected /embed, got %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}

		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode request: %v", err)
		}

		if len(req.Texts) != 1 {
			t.Errorf("Expected 1 text, got %d", len(req.Texts))
		}

		resp := embedResponse{
			Embeddings: [][]float32{{0.1, 0.2, 0.3}},
			Model:      "all-MiniLM-L6-v2",
			Dimensions: 3,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client
	client := NewClient(server.URL)

	// Test
	ctx := context.Background()
	embedding, err := client.GenerateEmbedding(ctx, "test text")
	if err != nil {
		t.Fatalf("GenerateEmbedding failed: %v", err)
	}

	if len(embedding) != 3 {
		t.Errorf("Expected 3 dimensions, got %d", len(embedding))
	}
}

func TestGenerateEmbeddings(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode request: %v", err)
		}

		// Create mock embeddings for each text
		embeddings := make([][]float32, len(req.Texts))
		for i := range req.Texts {
			embeddings[i] = []float32{float32(i), float32(i + 1), float32(i + 2)}
		}

		resp := embedResponse{
			Embeddings: embeddings,
			Model:      "all-MiniLM-L6-v2",
			Dimensions: 3,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client
	client := NewClient(server.URL)

	// Test with multiple texts
	ctx := context.Background()
	texts := []string{"text1", "text2", "text3"}
	embeddings, err := client.GenerateEmbeddings(ctx, texts)
	if err != nil {
		t.Fatalf("GenerateEmbeddings failed: %v", err)
	}

	if len(embeddings) != 3 {
		t.Errorf("Expected 3 embeddings, got %d", len(embeddings))
	}

	for i, emb := range embeddings {
		if len(emb) != 3 {
			t.Errorf("Embedding %d: expected 3 dimensions, got %d", i, len(emb))
		}
	}
}

func TestGenerateEmbeddingsEmptyInput(t *testing.T) {
	client := NewClient("http://localhost:8080")
	ctx := context.Background()

	_, err := client.GenerateEmbeddings(ctx, []string{})
	if err == nil {
		t.Error("Expected error for empty input, got nil")
	}
}

func TestGenerateEmbeddingsTooManyTexts(t *testing.T) {
	client := NewClient("http://localhost:8080")
	ctx := context.Background()

	// Create 101 texts (exceeds max of 100)
	texts := make([]string, 101)
	for i := range texts {
		texts[i] = "test"
	}

	_, err := client.GenerateEmbeddings(ctx, texts)
	if err == nil {
		t.Error("Expected error for too many texts, got nil")
	}
}

func TestHealthCheck(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("Expected /health, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("Expected GET, got %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "healthy",
			"model":      "all-MiniLM-L6-v2",
			"dimensions": 384,
		})
	}))
	defer server.Close()

	// Create client
	client := NewClient(server.URL)

	// Test
	ctx := context.Background()
	err := client.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
}

func TestTimeout(t *testing.T) {
	// Mock server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(15 * time.Second) // Exceeds 10s timeout
	}))
	defer server.Close()

	// Create client
	client := NewClient(server.URL)

	// Test with short context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := client.GenerateEmbedding(ctx, "test")
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}


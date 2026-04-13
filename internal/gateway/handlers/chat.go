package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/mrmushfiq/llm0-gateway/internal/gateway/auth"
	"github.com/mrmushfiq/llm0-gateway/internal/gateway/cache"
	"github.com/mrmushfiq/llm0-gateway/internal/gateway/cost"
	"github.com/mrmushfiq/llm0-gateway/internal/gateway/embeddings"
	"github.com/mrmushfiq/llm0-gateway/internal/gateway/failover"
	"github.com/mrmushfiq/llm0-gateway/internal/gateway/providers"
	"github.com/mrmushfiq/llm0-gateway/internal/gateway/ratelimit"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/config"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/database"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/models"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/redis"
)

// ChatHandler handles chat completion requests
type ChatHandler struct {
	db                *database.DB
	redis             *redis.Client
	cfg               *config.Config
	openaiProvider    *providers.OpenAIProvider
	anthropicProvider *providers.AnthropicProvider
	geminiProvider    *providers.GeminiProvider
	costCalculator    *cost.Calculator
	exactCache        *cache.ExactCache
	semanticCache     *cache.SemanticCache
	embeddingClient   *embeddings.Client
	failoverExecutor  *failover.Executor
	failoverLogger    *failover.Logger
	customerLimiter   *ratelimit.Limiter
}

// NewChatHandler creates a new chat handler
func NewChatHandler(db *database.DB, redis *redis.Client, cfg *config.Config) *ChatHandler {
	openaiProvider := providers.NewOpenAIProvider(cfg)
	anthropicProvider := providers.NewAnthropicProvider(cfg)
	geminiProvider := providers.NewGeminiProvider(cfg)

	handler := &ChatHandler{
		db:                db,
		redis:             redis,
		cfg:               cfg,
		openaiProvider:    openaiProvider,
		anthropicProvider: anthropicProvider,
		geminiProvider:    geminiProvider,
		costCalculator:    cost.NewCalculator(db),
		exactCache:        cache.NewExactCache(redis, db),
		failoverExecutor:  failover.NewExecutor(),
		failoverLogger:    failover.NewLogger(db),
		customerLimiter:   ratelimit.NewLimiter(redis, db),
	}

	// Register providers with failover executor
	handler.failoverExecutor.RegisterProvider("openai", func() failover.Provider {
		return openaiProvider
	})
	handler.failoverExecutor.RegisterProvider("anthropic", func() failover.Provider {
		return anthropicProvider
	})
	handler.failoverExecutor.RegisterProvider("google", func() failover.Provider {
		return geminiProvider
	})

	// Initialize embedding client and semantic cache if configured
	if cfg.EmbeddingServiceURL != "" {
		handler.embeddingClient = embeddings.NewClient(cfg.EmbeddingServiceURL)
		handler.semanticCache = cache.NewSemanticCache(
			db,
			handler.embeddingClient,
			0.95,        // Default similarity threshold
			1*time.Hour, // Default TTL
		)
		fmt.Printf("✅ Semantic cache initialized (URL: %s)\n", cfg.EmbeddingServiceURL)
	} else {
		fmt.Println("⚠️  Semantic cache disabled (no EMBEDDING_SERVICE_URL)")
	}

	fmt.Println("✅ Failover executor initialized with 3 providers")

	return handler
}

// LLMProvider interface for provider abstraction
type LLMProvider interface {
	ChatCompletion(ctx context.Context, req providers.ChatRequest) (*providers.ChatResponse, error)
	ValidateModel(model string) bool
}

// detectProvider detects which provider to use based on model name
func (h *ChatHandler) detectProvider(model string) (string, LLMProvider) {
	// Check OpenAI first (most common)
	if h.openaiProvider.ValidateModel(model) {
		return "openai", h.openaiProvider
	}

	// Check Anthropic
	if h.anthropicProvider.ValidateModel(model) {
		return "anthropic", h.anthropicProvider
	}

	// Check Gemini
	if h.geminiProvider.ValidateModel(model) {
		return "google", h.geminiProvider
	}

	// Model not found in any provider
	return "", nil
}

// ChatCompletions handles POST /v1/chat/completions
func (h *ChatHandler) ChatCompletions(c *gin.Context) {
	startTime := time.Now()

	// Get validated API key from auth middleware
	apiKey, ok := auth.GetAPIKey(c)
	if !ok {
		c.JSON(500, gin.H{"error": "internal_error", "message": "API key not found in context"})
		return
	}

	// Parse request
	var req providers.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	// If streaming is requested, route to streaming handler
	if req.Stream {
		// Store request in context so streaming handler can access it
		c.Set("parsed_request", req)
		h.ChatCompletionsStream(c)
		return
	}

	// Detect provider and validate model
	providerName, provider := h.detectProvider(req.Model)
	if provider == nil {
		c.JSON(400, gin.H{"error": "invalid_model", "message": fmt.Sprintf("Model %s is not supported", req.Model)})
		return
	}

	fmt.Printf("📡 Routing to %s for model %s\n", providerName, req.Model)

	// Get failover chain for this model (Pro tier feature)
	// For MVP: All users get failover (we'll add tier checks later)
	// Note: Failover is disabled for streaming requests (can't retry mid-stream)
	chain := failover.GetFailoverChain(req.Model)
	if chain != nil && !req.Stream {
		fmt.Printf("🔄 Failover enabled: %d providers in chain\n", len(chain.Steps))
	} else if req.Stream {
		fmt.Println("⚡ Streaming mode: failover disabled")
		chain = nil
	}

	ctx := c.Request.Context()

	// Step 1: Check rate limit
	rateLimitKey := fmt.Sprintf("ratelimit:key:%s", apiKey.KeyID)
	allowed, remaining, resetTime, err := h.redis.CheckRateLimit(
		ctx,
		rateLimitKey,
		apiKey.RateLimitPerMinute, // capacity
		apiKey.RateLimitPerMinute, // refill rate (per minute)
		1,                         // requested (1 request)
	)

	if err != nil {
		fmt.Printf("⚠️ Rate limit check failed: %v (fail-open)\n", err)
	} else if !allowed {
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", apiKey.RateLimitPerMinute))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime))
		c.JSON(429, gin.H{
			"error":       "rate_limit_exceeded",
			"message":     "Too many requests. Please try again later.",
			"retry_after": resetTime,
		})
		return
	}

	// Step 2: Check customer rate limits (if customer_id provided)
	customerID := c.GetHeader("X-Customer-ID")
	var customerLabels models.Labels
	var customerLimitCheck *ratelimit.CheckResult

	if customerID != "" {
		// Extract custom labels from headers (X-LLM0-*)
		customerLabels = make(models.Labels)
		for key, values := range c.Request.Header {
			if len(key) > 7 && key[:7] == "X-Llm0-" {
				labelKey := key[7:] // Remove "X-LLM0-" prefix
				if len(values) > 0 {
					customerLabels[labelKey] = values[0]
				}
			}
		}

		// Estimate cost for this request (for pre-check)
		estimatedCost := h.estimateRequestCost(req.Model, req.Messages)

		// Check customer limits
		customerLimitCheck, err = h.customerLimiter.Check(ctx, &ratelimit.CheckRequest{
			ProjectID:  apiKey.ProjectID,
			CustomerID: customerID,
			Model:      req.Model,
			CostUSD:    estimatedCost,
			Labels:     customerLabels,
		})
		if err != nil {
			fmt.Printf("⚠️ Customer rate limit check failed: %v (fail-open)\n", err)
		} else if !customerLimitCheck.Allowed {
			// Add customer limit headers
			for k, v := range customerLimitCheck.Headers {
				c.Header(k, v)
			}

			c.JSON(429, gin.H{
				"error":       "customer_rate_limit_exceeded",
				"message":     customerLimitCheck.Reason,
				"customer_id": customerID,
			})
			return
		}

		// Add customer spend headers (even if allowed)
		if customerLimitCheck != nil {
			if customerLimitCheck.DailySpendLimit != nil {
				c.Header("X-Customer-Spend-Today", fmt.Sprintf("%.4f", customerLimitCheck.DailySpend))
				c.Header("X-Customer-Limit-Daily", fmt.Sprintf("%.2f", *customerLimitCheck.DailySpendLimit))
				remaining := *customerLimitCheck.DailySpendLimit - customerLimitCheck.DailySpend
				c.Header("X-Customer-Remaining-Usd", fmt.Sprintf("%.4f", remaining))
			}

			// Add custom warning headers
			for k, v := range customerLimitCheck.Headers {
				c.Header(k, v)
			}
		}
	}

	// Step 3: Check exact match cache (if enabled)
	var cacheHit bool
	var semanticCacheHit bool
	var similarityScore float32
	var cachedResponse *providers.ChatResponse

	if apiKey.CacheEnabled {
		// 2a. Try exact cache first (< 1ms)
		cacheKey, err := h.exactCache.CacheKey(apiKey.ProjectID, providerName, req.Model, req.Messages)
		if err == nil {
			cachedResponse, cacheHit, err = h.exactCache.Get(ctx, cacheKey)
			if err != nil {
				fmt.Printf("⚠️ Exact cache check failed: %v\n", err)
			}

			if cacheHit {
				// Return exact cached response immediately
				// Cache hits cost $0 since we're not calling the LLM API
				cachedResponse.LatencyMs = int(time.Since(startTime).Milliseconds())
				cachedResponse.CostUSD = 0 // Cache hits are free
				c.Header("X-Cache-Hit", "exact")
				c.Header("X-Cost-USD", "0.000000") // Cache hits cost $0
				c.Header("X-Tokens-Prompt", fmt.Sprintf("%d", cachedResponse.Usage.PromptTokens))
				c.Header("X-Tokens-Completion", fmt.Sprintf("%d", cachedResponse.Usage.CompletionTokens))
				c.Header("X-Tokens-Total", fmt.Sprintf("%d", cachedResponse.Usage.TotalTokens))
				c.Header("X-Provider", providerName)
				c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
				c.JSON(200, cachedResponse)

				// Log cache hit in background
				go h.logRequest(context.Background(), apiKey, providerName, req, cachedResponse, true, false, 0, nil, "", customerID, customerLabels)
				return
			}
		}

		// 2b. Try semantic cache if exact miss and semantic is enabled (< 20ms)
		fmt.Printf("🔍 Semantic cache check: enabled=%v, cache=%v\n", apiKey.SemanticCacheEnabled, h.semanticCache != nil)
		if apiKey.SemanticCacheEnabled && h.semanticCache != nil {
			fmt.Println("🔎 Checking semantic cache...")
			cachedResponse, semanticCacheHit, similarityScore, err = h.semanticCache.Get(
				ctx, apiKey.ProjectID, providerName, req.Model, req.Messages)
			if err != nil {
				fmt.Printf("⚠️ Semantic cache check failed: %v\n", err)
			}

			if semanticCacheHit {
				// Return semantically similar cached response
				cachedResponse.LatencyMs = int(time.Since(startTime).Milliseconds())
				c.Header("X-Cache-Hit", "semantic")
				c.Header("X-Cache-Similarity", fmt.Sprintf("%.3f", similarityScore))
				c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
				c.JSON(200, cachedResponse)

				// Log cache hit in background
				go h.logRequest(context.Background(), apiKey, providerName, req, cachedResponse, false, true, similarityScore, nil, "", customerID, customerLabels)
				return
			}
		}
	}

	// Step 3: Check spend cap BEFORE making API call
	spendKey := fmt.Sprintf("spend:project:%s:%s", apiKey.ProjectID, time.Now().Format("2006-01"))

	// Estimate cost (conservative)
	estimatedTokens := 1000 // Conservative estimate
	estimatedCost, err := h.costCalculator.EstimateCost(providerName, req.Model, estimatedTokens)
	if err != nil {
		fmt.Printf("⚠️ Cost estimation failed: %v\n", err)
		estimatedCost = 0.10 // Fallback estimate
	}

	// Check if we can afford this request
	canAfford, currentSpend, cap, err := h.redis.CheckSpendCap(ctx, spendKey, estimatedCost, apiKey.MonthlyCap)
	if err != nil {
		fmt.Printf("⚠️ Spend cap check failed: %v (fail-open)\n", err)
	} else if !canAfford {
		c.JSON(402, gin.H{
			"error":         "spend_cap_exceeded",
			"message":       "Monthly spend cap reached",
			"current_spend": currentSpend,
			"monthly_cap":   cap,
		})
		return
	}

	// Step 4: Make request to LLM provider (with failover)
	requestID := uuid.New().String()
	failoverResult := h.failoverExecutor.Execute(ctx, req, chain)

	if !failoverResult.Success {
		c.JSON(500, gin.H{
			"error":   "provider_error",
			"message": failoverResult.Error.Error(),
		})
		// Log error in background
		go h.logRequest(context.Background(), apiKey, providerName, req, nil, false, false, 0, failoverResult, requestID, customerID, customerLabels)
		return
	}

	// Extract response and final provider
	response := failoverResult.Response
	finalProvider := failoverResult.FinalProvider

	// Log failover event if it occurred
	if failoverResult.FailoverOccurred {
		fmt.Printf("✅ Failover succeeded: %s/%s -> %s/%s\n",
			failoverResult.OriginalProvider, failoverResult.OriginalModel,
			failoverResult.FinalProvider, failoverResult.FinalModel)

		// Log failover to database (background)
		go h.failoverLogger.LogFailover(context.Background(), apiKey.ProjectID, requestID, failoverResult)
	}

	// Step 5: Calculate actual cost (use final provider/model after failover)
	actualCost, err := h.costCalculator.CalculateCost(
		finalProvider,
		failoverResult.FinalModel,
		response.Usage.PromptTokens,
		response.Usage.CompletionTokens,
	)
	if err != nil {
		fmt.Printf("⚠️ Cost calculation failed: %v\n", err)
		actualCost = estimatedCost // Use estimate as fallback
	}

	// Step 6: Track actual spend (adjust for estimate)
	spendAdjustment := actualCost - estimatedCost
	if spendAdjustment != 0 {
		_, _, _, err = h.redis.CheckSpendCap(ctx, spendKey, spendAdjustment, apiKey.MonthlyCap)
		if err != nil {
			fmt.Printf("⚠️ Spend adjustment failed: %v\n", err)
		}
	}

	// Step 7: Store in caches (if enabled) - use final provider/model after failover
	if apiKey.CacheEnabled {
		// Store in exact cache
		cacheKey, err := h.exactCache.CacheKey(apiKey.ProjectID, finalProvider, failoverResult.FinalModel, req.Messages)
		if err == nil {
			cacheTTL := apiKey.CacheTTL
			if cacheTTL == 0 {
				cacheTTL = h.cfg.CacheTTLSeconds
			}
			if err := h.exactCache.Set(ctx, apiKey.ProjectID, cacheKey, finalProvider, failoverResult.FinalModel, response, cacheTTL); err != nil {
				fmt.Printf("⚠️ Failed to cache in exact cache: %v\n", err)
			}
		}

		// Store in semantic cache (if enabled)
		if apiKey.SemanticCacheEnabled && h.semanticCache != nil {
			fmt.Println("💾 Storing in semantic cache...")
			if err := h.semanticCache.Set(ctx, apiKey.ProjectID, finalProvider, failoverResult.FinalModel, req.Messages, response); err != nil {
				fmt.Printf("⚠️ Failed to cache in semantic cache: %v\n", err)
			} else {
				fmt.Println("✅ Stored in semantic cache")
			}
		}
	}

	// Step 8: Log request in background
	go h.logRequest(context.Background(), apiKey, finalProvider, req, response, false, false, 0, failoverResult, requestID, customerID, customerLabels)

	// Step 8.5: Record customer request (if customer_id provided)
	if customerID != "" {
		err = h.customerLimiter.RecordRequest(ctx, &ratelimit.CheckRequest{
			ProjectID:  apiKey.ProjectID,
			CustomerID: customerID,
			Model:      failoverResult.FinalModel,
			CostUSD:    actualCost,
			Labels:     customerLabels,
		})
		if err != nil {
			fmt.Printf("⚠️ Failed to record customer request: %v\n", err)
		}
	}

	// Step 9: Return response with cost
	totalLatency := int(time.Since(startTime).Milliseconds())
	response.LatencyMs = totalLatency
	response.CostUSD = actualCost
	response.LatencyMs = totalLatency

	c.Header("X-Cache-Hit", "miss")
	c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
	c.Header("X-Cost-USD", fmt.Sprintf("%.6f", actualCost))
	c.Header("X-Tokens-Prompt", fmt.Sprintf("%d", response.Usage.PromptTokens))
	c.Header("X-Tokens-Completion", fmt.Sprintf("%d", response.Usage.CompletionTokens))
	c.Header("X-Tokens-Total", fmt.Sprintf("%d", response.Usage.TotalTokens))
	c.Header("X-Provider", finalProvider) // Show which provider was used
	if failoverResult.FailoverOccurred {
		c.Header("X-Failover", "true")
		c.Header("X-Original-Provider", failoverResult.OriginalProvider)
	}
	c.JSON(200, response)

	fmt.Printf("✅ Request completed in %dms (provider=%s, cost=$%.6f)\n",
		totalLatency, finalProvider, actualCost)
}

// logRequest logs the request to the database
func (h *ChatHandler) logRequest(
	ctx context.Context,
	apiKey *models.CachedAPIKey,
	provider string,
	req providers.ChatRequest,
	resp *providers.ChatResponse,
	exactCacheHit bool,
	semanticCacheHit bool,
	similarityScore float32,
	failoverResult *failover.FailoverResult,
	requestID string,
	customerID string,
	customerLabels models.Labels,
) {
	// Calculate cost
	var costUSD float64
	var promptTokens, completionTokens, totalTokens int

	if resp != nil {
		promptTokens = resp.Usage.PromptTokens
		completionTokens = resp.Usage.CompletionTokens
		totalTokens = resp.Usage.TotalTokens

		cost, err := h.costCalculator.CalculateCost(provider, req.Model, promptTokens, completionTokens)
		if err == nil {
			costUSD = cost
		}
	}

	// Failover metadata
	failoverOccurred := false
	finalProvider := provider
	failoverCount := 0

	if failoverResult != nil {
		failoverOccurred = failoverResult.FailoverOccurred
		finalProvider = failoverResult.FinalProvider
		failoverCount = failoverResult.AttemptsCount - 1 // Number of failover attempts
	}

	// Insert log (column names match schema)
	query := `
		INSERT INTO gateway_logs (
			id, project_id, api_key_id, provider, model,
			status, tokens_in, tokens_out, tokens_total,
			latency_ms, cost_usd, cache_hit, semantic_cache_hit, similarity_score,
			failover_occurred, final_provider, failover_count,
			customer_id, labels,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, NOW())
	`

	latencyMs := 0
	status := "success"
	if resp != nil {
		latencyMs = resp.LatencyMs
	}
	if failoverResult != nil && !failoverResult.Success {
		status = "error"
	}

	// Determine if any cache was hit
	anyCacheHit := exactCacheHit || semanticCacheHit

	// Convert customer_id (string) to sql.NullString
	var customerIDNull sql.NullString
	if customerID != "" {
		customerIDNull = sql.NullString{String: customerID, Valid: true}
	}

	// Convert labels to JSONB (or NULL)
	var labelsJSON interface{}
	if len(customerLabels) > 0 {
		labelsJSON = customerLabels
	}

	_, err := h.db.ExecContext(ctx, query,
		uuid.New(),
		apiKey.ProjectID,
		apiKey.KeyID,
		provider,
		req.Model,
		status,
		promptTokens,
		completionTokens,
		totalTokens,
		latencyMs,
		costUSD,
		anyCacheHit,
		semanticCacheHit,
		similarityScore,
		failoverOccurred,
		finalProvider,
		failoverCount,
		customerIDNull,
		labelsJSON,
	)

	if err != nil {
		fmt.Printf("⚠️ Failed to log request: %v\n", err)
	}
}

// estimateRequestCost estimates the cost of a request before making it
// This is used for pre-checking customer cost limits
func (h *ChatHandler) estimateRequestCost(model string, messages interface{}) float64 {
	// Rough token estimation: 4 characters = 1 token
	// We use interface{} for messages to avoid import cycles
	// Count characters from all messages (rough approximation)
	var totalChars int

	// Try to extract content from messages (works for most message types)
	// This is a simple heuristic - for production, you'd want more accurate token counting
	switch msgs := messages.(type) {
	case string:
		totalChars = len(msgs)
	default:
		// For complex message types, use a conservative estimate
		totalChars = 500 // Assume ~125 tokens average
	}

	estimatedPromptTokens := totalChars / 4
	if estimatedPromptTokens < 50 {
		estimatedPromptTokens = 50 // Minimum estimate
	}

	// Assume average completion length (adjust based on your use case)
	estimatedCompletionTokens := 150

	// Get pricing from database
	cost, err := h.costCalculator.CalculateCost("", model, estimatedPromptTokens, estimatedCompletionTokens)
	if err != nil {
		// Fallback to a conservative estimate if pricing not found
		// Assume GPT-4 pricing as the highest
		return 0.05 // $0.05 per request as safe upper bound
	}

	return cost
}

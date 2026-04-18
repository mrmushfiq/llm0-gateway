package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/mrmushfiq/llm0-gateway/internal/gateway/auth"
	"github.com/mrmushfiq/llm0-gateway/internal/gateway/handlers"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/config"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/database"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/redis"
	tlsConfig "github.com/mrmushfiq/llm0-gateway/internal/shared/tls"
)

func main() {
	// Load .env file (ignore error if not found)
	_ = godotenv.Load()

	// Load configuration
	cfg := config.Load()

	// Set Gin mode
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize components
	ctx := context.Background()

	// Connect to database
	db, err := database.NewPostgresDB(cfg)
	if err != nil {
		log.Fatalf("❌ Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Connect to Redis with optimized pool
	redisClient, err := redis.NewClient(ctx, cfg)
	if err != nil {
		log.Fatalf("❌ Failed to connect to Redis: %v", err)
	}
	defer redisClient.Close()

	// Initialize auth validator
	validator := auth.NewValidator(db, redisClient, cfg)
	authMiddleware := auth.NewMiddleware(validator)

	// Initialize handlers
	chatHandler := handlers.NewChatHandler(db, redisClient, cfg)
	healthHandler := handlers.NewHealthHandler(db, redisClient)

	// Create Gin router with optimizations
	router := gin.New()

	// Custom logger middleware (skip health checks)
	router.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{"/health", "/ready", "/live"},
	}))

	// Recovery middleware
	router.Use(gin.Recovery())

	// CORS middleware (configure as needed)
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Health check routes (no auth required)
	router.GET("/health", healthHandler.HealthCheck)
	router.GET("/ready", healthHandler.ReadyCheck)
	router.GET("/live", healthHandler.LiveCheck)

	// OpenAI-compatible v1 routes (auth required)
	v1 := router.Group("/v1")
	v1.Use(authMiddleware.RequireAPIKey())
	{
		v1.POST("/chat/completions", chatHandler.ChatCompletions)
		v1.GET("/models", chatHandler.ListModels)
	}

	// Create optimized HTTP server
	server := createOptimizedServer(router, cfg)

	// Start server in goroutine
	go func() {
		log.Printf("🚀 LLM0 Gateway API starting on port %s", cfg.Port)
		log.Printf("📊 Environment: %s", cfg.Environment)
		log.Printf("🔧 Redis pool: size=%d, idle=%d", cfg.RedisPoolSize, cfg.RedisMinIdleConns)

		if cfg.TLSEnabled {
			log.Printf("🔒 TLS 1.3 enabled with session caching")
			if err := server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				log.Fatalf("❌ Server failed to start: %v", err)
			}
		} else {
			log.Printf("⚠️  TLS disabled (development mode)")
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("❌ Server failed to start: %v", err)
			}
		}
	}()

	// Print banner
	printBanner(cfg)

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("🛑 Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("⚠️  Server forced to shutdown: %v", err)
	}

	log.Println("✅ Server exited gracefully")
}

// createOptimizedServer creates an HTTP server with TLS 1.3 and optimized timeouts
func createOptimizedServer(router *gin.Engine, cfg *config.Config) *http.Server {
	// Create TLS config if enabled
	tlsCfg, err := tlsConfig.CreateOptimizedTLSConfig(cfg)
	if err != nil {
		log.Printf("⚠️  TLS configuration failed: %v", err)
		tlsCfg = nil
	}

	return &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,

		// Timeouts optimized for LLM requests (can be slow)
		ReadTimeout:    30 * time.Second,  // Longer for large prompts
		WriteTimeout:   60 * time.Second,  // Longer for LLM responses
		IdleTimeout:    120 * time.Second, // Keep connections alive for reuse
		MaxHeaderBytes: 1 << 20,           // 1MB

		// TLS configuration
		TLSConfig: tlsCfg,
	}
}

// printBanner prints startup banner
func printBanner(cfg *config.Config) {
	fmt.Println("")
	fmt.Println("╔═══════════════════════════════════════════════════════════╗")
	fmt.Println("║                                                           ║")
	fmt.Println("║               🚀 LLM0 Gateway - Open Source 🚀            ║")
	fmt.Println("║                                                           ║")
	fmt.Println("║  OpenAI-compatible LLM proxy with:                        ║")
	fmt.Println("║  ✓ Multi-provider Failover (OpenAI / Anthropic / Google) ║")
	fmt.Println("║  ✓ Ollama Local Models  (FAILOVER_MODE configurable)     ║")
	fmt.Println("║  ✓ API Key Auth + Rate Limiting (Token Bucket)           ║")
	fmt.Println("║  ✓ Exact + Semantic Caching (Redis + pgvector)           ║")
	fmt.Println("║  ✓ Cost Tracking & Spend Caps                            ║")
	fmt.Println("║  ✓ GET /v1/models  (OpenAI-compatible model list)        ║")
	fmt.Println("║                                                           ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════╝")
	fmt.Println("")
	fmt.Printf("📍 Listening on:   http://localhost:%s\n", cfg.Port)
	fmt.Printf("📖 Health check:   http://localhost:%s/health\n", cfg.Port)
	fmt.Printf("🔑 Chat endpoint:  http://localhost:%s/v1/chat/completions\n", cfg.Port)
	fmt.Printf("📋 Models list:    http://localhost:%s/v1/models\n", cfg.Port)
	fmt.Printf("🤖 Failover mode:  %s\n", cfg.FailoverMode)
	if cfg.OllamaBaseURL != "" {
		fmt.Printf("🦙 Ollama:         %s\n", cfg.OllamaBaseURL)
	}
	fmt.Println("")
	fmt.Println("Press Ctrl+C to stop...")
	fmt.Println("")
}

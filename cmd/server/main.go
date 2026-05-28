package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/metall/mcp-web-scrape/internal/mcp"
	"github.com/metall/mcp-web-scrape/internal/pkg/browser"
	"github.com/metall/mcp-web-scrape/internal/pkg/cache"
	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/metall/mcp-web-scrape/internal/pkg/logger"
	"github.com/metall/mcp-web-scrape/internal/pkg/openapi"
	"github.com/metall/mcp-web-scrape/internal/pkg/proxy"
	"github.com/metall/mcp-web-scrape/internal/pkg/useragent"
	"github.com/rs/zerolog/log"
)

func main() {
	// Load configuration
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	if err := logger.Init(cfg.Log); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	log.Info().Msg("Starting MCP Web Scrape Server...")

	// Initialize cache
	cacheInstance, err := cache.New(cfg.Cache)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize cache")
	}

	// Initialize browser pool
	browserPool, err := browser.New(browser.Config{
		Logger:         log.Logger,
		MaxTabs:        cfg.Browser.MaxTabs,
		Headless:       cfg.Browser.Headless,
		DisableGPU:     cfg.Browser.DisableGPU,
		NoSandbox:      cfg.Browser.NoSandbox,
		ViewportWidth:  cfg.Browser.ViewportWidth,
		ViewportHeight: cfg.Browser.ViewportHeight,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize browser pool")
	}
	defer browserPool.Close()

	// Initialize User-Agent rotator
	uaRotator := useragent.New(useragent.Config{
		CustomUserAgents: cfg.UserAgent.CustomUserAgents,
	})
	log.Info().
		Int("total_uas", uaRotator.Count()).
		Msg("User-Agent rotator initialized")

	// Initialize proxy rotator
	proxyRotator, err := proxy.New(proxy.Config{
		Proxies:       cfg.Proxy.Proxies,
		Enabled:       cfg.Proxy.Enabled,
		TestOnStartup: cfg.Proxy.TestOnStartup,
		TestTimeout:   time.Duration(cfg.Proxy.TestTimeout) * time.Second,
		Logger:        log.Logger,
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to initialize proxy rotator, continuing without proxy")
		proxyRotator, _ = proxy.New(proxy.Config{
			Proxies: []string{},
			Enabled: false,
			Logger:  log.Logger,
		})
	} else {
		log.Info().
			Int("proxies", proxyRotator.GetCount()).
			Bool("enabled", proxyRotator.IsEnabled()).
			Msg("Proxy rotator initialized")
	}

	// Create MCP server
	mcpServer, err := mcp.New(mcp.Config{
		ProtocolVersion: "2024-11-05",
		ServerName:      "mcp-web-scrape",
		ServerVersion:   "1.0.0",
		RateLimit: mcp.RateLimitConfig{
			RequestsPerSecond: cfg.RateLimit.RequestsPerSecond,
			BurstSize:         cfg.RateLimit.BurstSize,
			Enabled:           cfg.RateLimit.Enabled,
		},
		Cache:       cacheInstance,
		BrowserPool: browserPool,
		RAG:         cfg.RAG,
		UARotator:   uaRotator,
		ProxyRotator: proxyRotator,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create MCP server")
	}

	// Create transport
	transport := mcp.NewTransport(mcpServer)

	// Create OpenAPI handler for Open WebUI compatibility
	baseURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	openapiHandler := openapi.NewHandler(mcpServer, baseURL)
	openapiHandler.SetLogger(log.Logger)

	// Setup Gin router
	if cfg.Log.Level == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(loggerMiddleware())

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"time":   time.Now().Unix(),
		})
	})
	router.HEAD("/health", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// MCP endpoint
	router.Any(cfg.MCP.Endpoint, func(c *gin.Context) {
		transport.HandleMCP(c.Writer, c.Request)
	})

	// SSE endpoint for llama.cpp WebUI compatibility
	router.Any("/sse", func(c *gin.Context) {
		transport.HandleMCP(c.Writer, c.Request)
	})

	// Register OpenAPI/REST endpoints for Open WebUI compatibility
	openapiHandler.RegisterRoutes(router)

	// API info
	router.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"name":        "MCP Web Scrape Server",
			"version":     "1.0.0",
			"description": "MCP server for web scraping and search capabilities",
			"endpoints": map[string]string{
				"mcp":     cfg.MCP.Endpoint,
				"health":  "/health",
				"metrics": "/metrics",
			},
			"capabilities": mcpServer.GetCapabilities(),
			"tools": []string{
				"scrape_url",
				"search_web",
				"parse_html",
			},
		})
	})

	// Metrics endpoint (basic)
	router.GET("/metrics", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"rate_limit":  mcpServer.GetRateLimitInfo(),
			"cache": gin.H{
				"enabled": cacheInstance.IsEnabled(),
			},
			"browser_pool": browserPool.GetStats(),
			"user_agent":   uaRotator.Stats(),
			"proxy":        proxyRotator.GetStats(),
		})
	})

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Graceful shutdown
	go func() {
		log.Info().Str("addr", addr).Msg("Server listening")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("Server forced to shutdown")
	}

	log.Info().Msg("Server stopped")
}

func loggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Log request
		latency := time.Since(start)
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()

		if raw != "" {
			path = path + "?" + raw
		}

		log.Info().
			Str("client_ip", clientIP).
			Str("method", method).
			Str("path", path).
			Int("status", statusCode).
			Dur("latency", latency).
			Msg("HTTP request")
	}
}

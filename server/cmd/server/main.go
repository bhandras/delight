package main

import (
	"fmt"
	"os"

	"github.com/bhandras/delight/server/internal/api/handlers"
	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/config"
	"github.com/bhandras/delight/server/internal/crypto"
	"github.com/bhandras/delight/server/internal/database"
	"github.com/bhandras/delight/server/internal/database/migrations"
	"github.com/bhandras/delight/server/internal/debug"
	"github.com/bhandras/delight/server/internal/websocket"
	"github.com/bhandras/delight/shared/logger"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Errorf("Failed to load config: %v", err)
		os.Exit(1)
	}

	if cfg.Debug {
		logger.SetLevel(logger.LevelDebug)
	}

	// Set Gin mode
	if !cfg.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	// Open database
	logger.Infof("Opening database: %s", cfg.DatabasePath)
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		logger.Errorf("Failed to open database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// Dev-only: prune all messages to clear bad legacy payloads
	if os.Getenv("DELIGHT_DEV_PRUNE_MESSAGES") == "1" || os.Getenv("DELIGHT_DEV_PRUNE_MESSAGES") == "true" {
		logger.Warnf("DELIGHT_DEV_PRUNE_MESSAGES enabled - pruning session_messages table")
		if err := debug.PruneMessages(db.DB); err != nil {
			logger.Warnf("Failed to prune messages: %v", err)
		}
	}

	// Migrate legacy message content (wrap non-JSON content)
	if err := migrations.WrapLegacyMessageContent(db.DB); err != nil {
		logger.Warnf("Failed to wrap legacy message content: %v", err)
	}

	// Initialize JWT manager
	logger.Infof("Initializing JWT manager...")
	jwtManager, err := crypto.NewJWTManager(cfg.MasterSecret)
	if err != nil {
		logger.Errorf("Failed to create JWT manager: %v", err)
		os.Exit(1)
	}

	// Initialize Socket.IO server
	logger.Infof("Initializing Socket.IO server...")
	socketIOServer := websocket.NewSocketIOServer(db.DB, jwtManager)
	defer socketIOServer.Close()

	// Create Gin router
	router := gin.Default()

	// CORS middleware
	router.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	// Logging middleware
	router.Use(middleware.LoggingMiddleware())

	// Root endpoint - returns plain text for client validation
	router.GET("/", func(c *gin.Context) {
		c.String(200, "Welcome to Delight Server!")
	})

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(db.DB, jwtManager)
	sessionHandler := handlers.NewSessionHandler(db.DB, socketIOServer)
	terminalHandler := handlers.NewTerminalHandler(db.DB, socketIOServer)
	userHandler := handlers.NewUserHandler(db.DB, socketIOServer)
	kvHandler := handlers.NewKVHandler(db.DB, socketIOServer)
	artifactHandler := handlers.NewArtifactHandler(db.DB, socketIOServer)
	feedHandler := handlers.NewFeedHandler(db.DB)

	// Public routes (no auth required)
	v1 := router.Group("/v1")
	{
		v1.POST("/auth", authHandler.PostAuth)
		v1.POST("/auth/request", authHandler.PostAuthRequest)
		v1.GET("/auth/request/status", authHandler.GetAuthRequestStatus)
		v1.POST("/auth/account/request", authHandler.PostAccountAuthRequest)

		// Stub endpoints for compatibility
		v1.POST("/version", func(c *gin.Context) {
			c.JSON(200, gin.H{"version": "1.0.0"})
		})
	}

	// Protected routes (auth required)
	protected := v1.Group("")
	protected.Use(middleware.AuthMiddleware(jwtManager))
	{
		// Auth
		protected.POST("/auth/response", authHandler.PostAuthResponse)
		protected.POST("/auth/account/response", authHandler.PostAccountAuthResponse)

		// Feed
		protected.GET("/feed", feedHandler.ListFeed)

		// Sessions
		protected.GET("/sessions", sessionHandler.ListSessions)
		protected.POST("/sessions", sessionHandler.CreateSession)
		protected.GET("/sessions/:id", sessionHandler.GetSession)
		protected.DELETE("/sessions/:id", sessionHandler.DeleteSession)
		protected.GET("/sessions/:id/messages", sessionHandler.GetSessionMessages)

		// Terminals
		protected.GET("/terminals", terminalHandler.ListTerminals)
		protected.POST("/terminals", terminalHandler.CreateTerminal)
		protected.GET("/terminals/:id", terminalHandler.GetTerminal)
		protected.DELETE("/terminals/:id", terminalHandler.DeleteTerminal)
		protected.POST("/terminals/:id/alive", terminalHandler.KeepAlive)

		// User
		protected.GET("/user", userHandler.GetProfile)
		protected.POST("/user/profile", userHandler.UpdateProfile)
		protected.POST("/user/settings", userHandler.UpdateSettings)
		protected.DELETE("/user/avatar", userHandler.DeleteAvatar)

		// Stub endpoints for compatibility
		protected.GET("/account/profile", userHandler.GetProfile)
		protected.GET("/account/settings", userHandler.GetSettings)
		protected.POST("/account/settings", userHandler.UpdateSettings)
		protected.GET("/artifacts", artifactHandler.ListArtifacts)
		protected.GET("/artifacts/:id", artifactHandler.GetArtifact)
		protected.POST("/artifacts", artifactHandler.CreateArtifact)
		protected.POST("/artifacts/:id", artifactHandler.UpdateArtifact)
		protected.DELETE("/artifacts/:id", artifactHandler.DeleteArtifact)
		protected.POST("/push-tokens", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true})
		})

		// KV store
		protected.GET("/kv", kvHandler.ListKV)
		protected.GET("/kv/:key", kvHandler.GetKV)
		protected.POST("/kv", kvHandler.MutateKV)
		protected.POST("/kv/bulk", kvHandler.BulkGetKV)
	}

	// V2 endpoints
	v2 := router.Group("/v2")
	v2.Use(middleware.AuthMiddleware(jwtManager))
	{
		v2.GET("/sessions/active", sessionHandler.ListSessions)
	}

	// Mount Socket.IO endpoint at /v1/updates (accessible without auth for handshake)
	// Auth will be checked after connection is established
	router.Any("/v1/updates", socketIOServer.HandleSocketIO())
	router.Any("/v1/updates/*any", socketIOServer.HandleSocketIO())

	// Start HTTP server
	addr := fmt.Sprintf(":%d", cfg.Port)
	logger.Infof("Delight Server starting on http://localhost%s", addr)
	logger.Infof("Database: %s", cfg.DatabasePath)
	logger.Infof("JWT signing enabled")

	if err := router.Run(addr); err != nil {
		logger.Errorf("Failed to start server: %v", err)
		os.Exit(1)
	}
}

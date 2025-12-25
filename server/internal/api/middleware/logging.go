package middleware

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

// LoggingMiddleware logs HTTP requests
func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Calculate latency
		latency := time.Since(start)

		// Get status code
		statusCode := c.Writer.Status()

		// Log format: [method] path?query - status (latency)
		if raw != "" {
			path = path + "?" + raw
		}

		log.Printf("[%s] %s - %d (%v)", c.Request.Method, path, statusCode, latency)
	}
}

package api

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Constants for request ID middleware
const (
	RequestIDHeader = "X-Request-ID"
	RequestIDKey    = "request_id"
)

// APIKeyAuth returns a middleware that validates API key authentication.
// It checks for the API key in the X-API-Key header first, then falls back
// to the api_key query parameter.
//
// If apiKey is empty, the middleware allows all requests through (for development).
// Uses constant-time comparison to prevent timing attacks.
func APIKeyAuth(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// If no API key is configured, allow all requests
		if apiKey == "" {
			c.Next()
			return
		}

		// Check header first
		key := c.GetHeader("X-API-Key")
		if key == "" {
			// Fall back to query parameter
			key = c.Query("api_key")
		}

		// Use constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "unauthorized",
				Code:  "UNAUTHORIZED",
			})
			return
		}

		c.Next()
	}
}

// RequestID middleware adds a unique request ID to each request for distributed tracing.
// If the client provides an X-Request-ID header, it is reused. Otherwise, a new UUID is generated.
// The request ID is stored in the gin context and returned in the response header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Use existing header if provided, otherwise generate
		requestID := c.GetHeader(RequestIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Store in context and set response header
		c.Set(RequestIDKey, requestID)
		c.Header(RequestIDHeader, requestID)

		c.Next()
	}
}

// GetRequestID extracts the request ID from a gin context.
// Returns an empty string if no request ID is present.
func GetRequestID(c *gin.Context) string {
	if id, exists := c.Get(RequestIDKey); exists {
		return id.(string)
	}
	return ""
}

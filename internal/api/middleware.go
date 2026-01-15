package api

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
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

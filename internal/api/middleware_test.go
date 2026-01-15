package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIKeyAuth_ValidHeaderKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(APIKeyAuth("test-secret-key"))
	router.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "test-secret-key")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if w.Body.String() != "success" {
		t.Errorf("expected body 'success', got %q", w.Body.String())
	}
}

func TestAPIKeyAuth_ValidQueryParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(APIKeyAuth("test-secret-key"))
	router.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected?api_key=test-secret-key", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestAPIKeyAuth_MissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(APIKeyAuth("test-secret-key"))
	router.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAPIKeyAuth_InvalidKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(APIKeyAuth("test-secret-key"))
	router.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestAPIKeyAuth_HeaderTakesPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(APIKeyAuth("header-key"))
	router.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})

	// Query param has wrong key, header has correct key
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected?api_key=wrong-key", nil)
	req.Header.Set("X-API-Key", "header-key")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected header to take precedence, got status %d", w.Code)
	}
}

func TestAPIKeyAuth_EmptyAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// When the server has an empty API key configured, middleware should pass all requests
	router := gin.New()
	router.Use(APIKeyAuth(""))
	router.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d when API key not configured, got %d", http.StatusOK, w.Code)
	}
}

func TestAPIKeyAuth_TimingAttackResistance(t *testing.T) {
	// Verify constant-time comparison is used (can't directly test timing,
	// but we can verify the middleware works correctly for various key lengths)
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name      string
		serverKey string
		clientKey string
		wantCode  int
	}{
		{"same length wrong key", "secretkey", "wrongkeys", http.StatusUnauthorized},
		{"shorter client key", "secretkey", "short", http.StatusUnauthorized},
		{"longer client key", "secret", "longersecretkey", http.StatusUnauthorized},
		{"empty client key", "secretkey", "", http.StatusUnauthorized},
		{"correct key", "secretkey", "secretkey", http.StatusOK},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(APIKeyAuth(tc.serverKey))
			router.GET("/protected", func(c *gin.Context) {
				c.String(http.StatusOK, "success")
			})

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/protected", nil)
			req.Header.Set("X-API-Key", tc.clientKey)
			router.ServeHTTP(w, req)

			if w.Code != tc.wantCode {
				t.Errorf("expected status %d, got %d", tc.wantCode, w.Code)
			}
		})
	}
}

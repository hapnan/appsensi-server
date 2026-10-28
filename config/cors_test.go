package config

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORSMiddleware())
	r.POST("/register/begin", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestCORSPreflightAllowedOrigin(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	r := setupTestRouter()

	req := httptest.NewRequest(http.MethodOptions, "/register/begin", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("expected Access-Control-Allow-Origin 'http://localhost:3000', got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected Access-Control-Allow-Credentials 'true', got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Fatalf("expected Access-Control-Allow-Methods to include POST, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") {
		t.Fatalf("expected Access-Control-Allow-Headers to include Content-Type, got %q", got)
	}
}

func TestCORSPostAllowedOrigin(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	r := setupTestRouter()

	req := httptest.NewRequest(http.MethodPost, "/register/begin", strings.NewReader(`{"username":"123","email":"a@b.com"}`))
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("expected Access-Control-Allow-Origin 'http://localhost:3000', got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected Access-Control-Allow-Credentials 'true', got %q", got)
	}
}

func TestCORSPreflightDisallowedOrigin(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	r := setupTestRouter()

	req := httptest.NewRequest(http.MethodOptions, "/register/begin", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected empty Access-Control-Allow-Origin for disallowed origin, got %q", got)
	}
}

func TestGetAllowedOriginsEnvOverride(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", " http://localhost:3000/ , https://app.example.com/ ")

	got := GetAllowedOrigins()
	want := []string{"http://localhost:3000", "https://app.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

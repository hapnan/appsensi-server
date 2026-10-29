package config

import (
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

const defaultAllowedOrigin = "http://localhost:3000"

// IsConformanceEnabled reports whether FIDO Conformance test mode is enabled via FIDO_CONFORMANCE=true.
func IsConformanceEnabled() bool {
	v := strings.TrimSpace(os.Getenv("FIDO_CONFORMANCE"))
	return strings.EqualFold(v, "true") || v == "1"
}

// GetConformanceEndpoint returns the public/local base URL used by the FIDO Conformance Test Tool.
func GetConformanceEndpoint() string {
	if raw := strings.TrimRight(strings.TrimSpace(os.Getenv("FIDO_CONFORMANCE_ENDPOINT")), "/"); raw != "" {
		return raw
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	return "http://localhost:" + port
}

func extractOrigin(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// GetAllowedOrigins returns the configured list of allowed CORS and WebAuthn origins.
// It reads a comma-separated list from ALLOWED_ORIGINS and falls back to http://localhost:3000.
// When FIDO_CONFORMANCE=true, it also ensures the server's local/conformance origin is permitted.
func GetAllowedOrigins() []string {
	var origins []string
	raw := os.Getenv("ALLOWED_ORIGINS")
	if raw == "" {
		origins = []string{defaultAllowedOrigin}
	} else {
		parts := strings.Split(raw, ",")
		origins = make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimRight(strings.TrimSpace(part), "/")
			if trimmed != "" {
				origins = append(origins, trimmed)
			}
		}
		if len(origins) == 0 {
			origins = []string{defaultAllowedOrigin}
		}
	}

	if IsConformanceEnabled() {
		port := strings.TrimSpace(os.Getenv("PORT"))
		if port == "" {
			port = "8080"
		}
		extra := []string{"http://localhost:" + port}
		if epOrigin := extractOrigin(GetConformanceEndpoint()); epOrigin != "" {
			extra = append(extra, epOrigin)
		}
		for _, candidate := range extra {
			if !slices.Contains(origins, candidate) {
				origins = append(origins, candidate)
			}
		}
	}

	return origins
}

// CORSMiddleware handles CORS headers and OPTIONS preflight requests for configured origins.
func CORSMiddleware() gin.HandlerFunc {
	allowed := make(map[string]struct{})
	for _, origin := range GetAllowedOrigins() {
		allowed[origin] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		h := c.Writer.Header()
		h.Add("Vary", "Origin")
		if c.Request.Method == http.MethodOptions {
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
		}

		if origin != "" {
			if _, ok := allowed[origin]; ok {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept, Authorization, X-Requested-With")
				h.Set("Access-Control-Max-Age", "43200")

				if c.Request.Method == http.MethodOptions {
					c.AbortWithStatus(http.StatusNoContent)
					return
				}
			} else if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
		} else if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestNewHTTPServerAppliesTimeoutHardening(t *testing.T) {
	srv := NewHTTPServer("127.0.0.1:0", http.NewServeMux())
	if srv.ReadHeaderTimeout != serverReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout=%v want %v", srv.ReadHeaderTimeout, serverReadHeaderTimeout)
	}
	if srv.ReadTimeout != serverReadTimeout {
		t.Fatalf("ReadTimeout=%v want %v", srv.ReadTimeout, serverReadTimeout)
	}
	if srv.WriteTimeout != serverWriteTimeout {
		t.Fatalf("WriteTimeout=%v want %v", srv.WriteTimeout, serverWriteTimeout)
	}
	if srv.IdleTimeout != serverIdleTimeout {
		t.Fatalf("IdleTimeout=%v want %v", srv.IdleTimeout, serverIdleTimeout)
	}
	if srv.MaxHeaderBytes != serverMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes=%d want %d", srv.MaxHeaderBytes, serverMaxHeaderBytes)
	}
}

func TestHandlerAppliesBearerAuthWhenConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.Auth = "token"
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	srv := New(eng, "127.0.0.1", 0)
	srv.SetBearerToken("secret-token")
	handler := srv.Handler()

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	handler.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("GET / should stay public, got %d", rootRec.Code)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth should 401, got %d", apiRec.Code)
	}

	apiReq = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	apiReq.Header.Set("Authorization", "Bearer secret-token")
	apiRec = httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("bearer token should authorize, got %d", apiRec.Code)
	}
}

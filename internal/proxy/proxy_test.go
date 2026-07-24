package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyForwardsRequests(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(r.Method + " " + r.URL.Path))
	}))
	defer backend.Close()

	p, err := New(Config{Backend: backend.URL, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://proxy.test/orders?id=42", nil)
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("X-Upstream"); got != "ok" {
		t.Fatalf("header = %q", got)
	}
	body, _ := io.ReadAll(recorder.Result().Body)
	if string(body) != "POST /orders" {
		t.Fatalf("body = %q", body)
	}
}

func TestHealthAndMetrics(t *testing.T) {
	p, err := New(Config{Backend: "http://localhost:1"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://proxy.test/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://proxy.test/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", recorder.Code)
	}
}

func TestRateLimit(t *testing.T) {
	p, err := New(Config{Backend: "http://localhost:1", RateLimitPerSecond: 1})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://proxy.test/", nil)
	p.ServeHTTP(httptest.NewRecorder(), request)
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", recorder.Code)
	}
}

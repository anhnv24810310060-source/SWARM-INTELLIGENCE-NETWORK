package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	// use 1s interval to avoid division by zero
	r := newRateLimiter(2, 2, time.Second)
	if !r.allow("x") {
		t.Fatal("expected first allowed")
	}
	if !r.allow("x") {
		t.Fatal("expected second allowed")
	}
	if r.allow("x") {
		t.Fatal("expected third denied")
	}
}

func TestEchoRequiresAuth(t *testing.T) {
	mux := http.NewServeMux()
	lim := newRateLimiter(10, 10, 0)
	mux.HandleFunc("/v1/echo", func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(r) {
			w.WriteHeader(401)
			return
		}
		if !lim.allow(rateKey(r)) {
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/echo", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != 401 {
		t.Fatalf("expected 401 got %d", rw.Code)
	}
}

func TestIngestValidation(t *testing.T) {
	mux := http.NewServeMux()
	lim := newRateLimiter(10, 10, time.Second)
	mux.HandleFunc("/v1/ingest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if !authenticate(r) {
			w.WriteHeader(401)
			return
		}
		if !lim.allow(rateKey(r)) {
			w.WriteHeader(429)
			return
		}
		var body bytes.Buffer
		body.ReadFrom(r.Body)
		if !bytes.Contains(body.Bytes(), []byte("id")) {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(202)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest", bytes.NewBufferString(`{"id":"x","timestamp":123}`))
	req.Header.Set("Authorization", "Bearer dev")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != 202 {
		t.Fatalf("expected 202 got %d", rw.Code)
	}
}

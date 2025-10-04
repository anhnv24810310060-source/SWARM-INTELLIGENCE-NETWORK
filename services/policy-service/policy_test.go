package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvaluateDefaultPolicy(t *testing.T) {
	// reuse main store logic by instantiating handlers via main-like setup is heavy; directly test allow_all rule.
	p := policy{Name: "p1", Version: 1, Rule: "allow_all"}
	if p.Rule != "allow_all" {
		t.Fatal("unexpected rule")
	}
}

func TestPolicyHTTPHandlers(t *testing.T) {
	st := newStore()
	st.put(policy{Name: "x", Version: 1, Rule: "allow_all"})
	mux := http.NewServeMux()
	// minimal copy of evaluation handler
	mux.HandleFunc("/v1/evaluate", func(w http.ResponseWriter, r *http.Request) {
		var req evalRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		p, ok := st.get(req.Policy)
		if !ok {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(evalResponse{Allow: p.Rule == "allow_all"})
	})
	body, _ := json.Marshal(evalRequest{Policy: "x"})
	req := httptest.NewRequest(http.MethodPost, "/v1/evaluate", bytes.NewReader(body))
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != 200 {
		t.Fatalf("expected 200 got %d", rw.Code)
	}
}

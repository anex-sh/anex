package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type sampleResp struct {
	OK   bool   `json:"ok"`
	Name string `json:"name"`
}

func TestMakeRequestSuccessAndDecode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sampleResp{OK: true, Name: "alice"})
	}))
	defer ts.Close()

	rc := NewDefaultRetryClient()
	status, out, err := MakeRequest[sampleResp](context.Background(), rc, http.MethodGet, ts.URL, nil, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if status != 200 || !out.OK || out.Name != "alice" {
		t.Fatalf("unexpected result: status=%d out=%+v", status, out)
	}
}

func TestMakeRequestNon2xxReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(418)
		w.Write([]byte("teapot"))
	}))
	defer ts.Close()

	rc := NewDefaultRetryClient()
	status, _, err := MakeRequest[sampleResp](context.Background(), rc, http.MethodGet, ts.URL, nil, nil)
	if err == nil || status != 418 {
		t.Fatalf("expected error and status 418, got err=%v status=%d", err, status)
	}
}

func TestMakeRequestNoContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	rc := NewDefaultRetryClient()
	status, out, err := MakeRequest[sampleResp](context.Background(), rc, http.MethodGet, ts.URL, nil, nil)
	if err != nil || status != http.StatusNoContent || (out != sampleResp{}) {
		t.Fatalf("unexpected: status=%d out=%+v err=%v", status, out, err)
	}
}

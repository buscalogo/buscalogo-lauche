package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"buscalogo-agent/internal/config"
)

func TestAPIExposed(t *testing.T) {
	cases := []struct {
		listen  string
		exposed bool
	}{
		{"127.0.0.1:9970", false},
		{"localhost:9970", false},
		{"0.0.0.0:9970", true},
		{":9970", true},
		{"192.168.1.10:9970", true},
	}
	for _, tc := range cases {
		s := &Server{cfg: &config.Config{Data: config.Data{API: config.API{Listen: tc.listen}}}}
		if got := s.apiExposed(); got != tc.exposed {
			t.Fatalf("listen=%s exposed=%v want %v", tc.listen, got, tc.exposed)
		}
	}
}

func TestMutationAuthBearer(t *testing.T) {
	s := &Server{cfg: &config.Config{Data: config.Data{
		API: config.API{Listen: "0.0.0.0:9970", Token: "secret"},
	}}}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	h := s.mutationAuth(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/sites", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: got %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/sites", nil)
	req2.Header.Set("Authorization", "Bearer secret")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("bearer: got %d body=%s", rec2.Code, rec2.Body.String())
	}

	req3 := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("GET should pass: %d", rec3.Code)
	}
}

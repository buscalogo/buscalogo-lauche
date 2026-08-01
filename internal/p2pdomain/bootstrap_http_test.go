package p2pdomain

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchBootstrapRegistriesHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/registries.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"updated_at": "2026-08-01T12:00:00Z",
			"registries": []map[string]string{
				{"ygg_ip": "200::a", "name": "a"},
				{"ygg_ip": "200::a"}, // dup
				{"ygg_ip": "not-an-ip"},
				{"ygg_ip": "200::b"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ips, err := FetchBootstrapRegistries(t.Context(), srv.URL+"/registries.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 2 {
		t.Fatalf("ips=%v", ips)
	}
}

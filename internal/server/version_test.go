package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"diting/internal/store"
)

func TestVersionedAndLegacyRoutesCoexist(t *testing.T) {
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	server := &Server{store: segmentStore, mux: http.NewServeMux()}
	server.mux.HandleFunc("GET /api/stats", server.handleStats)
	server.mux.HandleFunc(versionedPattern("GET /api/stats"), server.handleStats)

	for _, path := range []string{"/api/stats", "/api/v1/stats"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = "127.0.0.1:8080"
		server.mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, recorder.Code)
		}
	}
}

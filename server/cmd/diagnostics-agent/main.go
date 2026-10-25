package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/diagnosticslog"
)

func main() {
	token := strings.TrimSpace(os.Getenv("MULTICA_DIAGNOSTICS_AGENT_TOKEN"))
	if token == "" {
		log.Fatal("MULTICA_DIAGNOSTICS_AGENT_TOKEN is required")
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8090"
	}
	collector := diagnosticslog.NewDockerCollector(
		os.Getenv("MULTICA_DIAGNOSTICS_DOCKER_SOCKET"),
		os.Getenv("MULTICA_DIAGNOSTICS_COMPOSE_PROJECT"),
		os.Getenv("MULTICA_DIAGNOSTICS_SERVICE_NAME"),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/logs", authorize(token, func(w http.ResponseWriter, r *http.Request) {
		q, ok := parseQuery(w, r, 5000)
		if !ok {
			return
		}
		resp, err := collector.Read(r.Context(), q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	mux.HandleFunc("GET /v1/export", authorize(token, func(w http.ResponseWriter, r *http.Request) {
		q, ok := parseQuery(w, r, 50000)
		if !ok {
			return
		}
		resp, err := collector.Read(r.Context(), q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		for _, entry := range resp.Entries {
			if err := enc.Encode(entry); err != nil {
				return
			}
		}
	}))

	server := &http.Server{
		Addr: ":" + port, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
	log.Printf("diagnostics agent listening on :%s", port)
	log.Fatal(server.ListenAndServe())
}

func authorize(token string, next http.HandlerFunc) http.HandlerFunc {
	expected := []byte("Bearer " + token)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(expected) || subtle.ConstantTimeCompare(got, expected) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func parseQuery(w http.ResponseWriter, r *http.Request, maxTail int) (diagnosticslog.Query, bool) {
	q := diagnosticslog.Query{
		Source: strings.TrimSpace(r.URL.Query().Get("source")),
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
		Tail: 1000,
	}
	if len(q.Source) > 128 || len(q.Search) > 500 {
		http.Error(w, "query too long", http.StatusBadRequest)
		return diagnosticslog.Query{}, false
	}
	if raw := r.URL.Query().Get("tail"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxTail {
			http.Error(w, "invalid tail", http.StatusBadRequest)
			return diagnosticslog.Query{}, false
		}
		q.Tail = n
	}
	if raw := r.URL.Query().Get("since"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "invalid since", http.StatusBadRequest)
			return diagnosticslog.Query{}, false
		}
		q.Since = n
	}
	return q, true
}

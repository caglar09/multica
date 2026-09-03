package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) ListDiagnosticLogs(w http.ResponseWriter, r *http.Request) {
	h.proxyDiagnostics(w, r, "/v1/logs", 5000, 30*time.Second, false)
}

func (h *Handler) ExportDiagnosticLogs(w http.ResponseWriter, r *http.Request) {
	h.proxyDiagnostics(w, r, "/v1/export", 50000, 2*time.Minute, true)
}

func (h *Handler) proxyDiagnostics(w http.ResponseWriter, r *http.Request, endpoint string, maxTail int, timeout time.Duration, download bool) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_DIAGNOSTICS_AGENT_URL")), "/")
	token := strings.TrimSpace(os.Getenv("MULTICA_DIAGNOSTICS_AGENT_TOKEN"))
	if base == "" || token == "" {
		writeError(w, http.StatusServiceUnavailable, "diagnostics log collector is not configured")
		return
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		writeError(w, http.StatusServiceUnavailable, "diagnostics log collector URL is invalid")
		return
	}

	values := url.Values{}
	for _, key := range []string{"source", "search", "tail", "since"} {
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			values.Set(key, value)
		}
	}
	if len(values.Get("source")) > 128 || len(values.Get("search")) > 500 {
		writeError(w, http.StatusBadRequest, "diagnostics query too long")
		return
	}
	if raw := values.Get("tail"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxTail {
			writeError(w, http.StatusBadRequest, "invalid diagnostics tail")
			return
		}
	}
	if raw := values.Get("since"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid diagnostics since")
			return
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+endpoint+"?"+values.Encode(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build diagnostics request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "diagnostics log collector is unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = fmt.Sprintf("diagnostics collector returned %d", resp.StatusCode)
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}

	if download {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"multica-logs-%s.ndjson\"", time.Now().UTC().Format("20060102T150405Z")))
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

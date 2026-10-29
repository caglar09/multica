package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeProjectDiscoveryReportAcceptsPlainAndFencedJSON(t *testing.T) {
	data, err := json.Marshal(map[string]any{
		"project": map[string]string{
			"title": "Inventory", "purpose": "Track stock", "goal": "Reduce errors", "architecture_summary": "An API",
		},
		"boundaries":           map[string]any{},
		"implemented_features": []any{},
		"next_tasks":           []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{string(data), "```json\n" + string(data) + "\n```"} {
		if _, err := decodeProjectDiscoveryReport(output); err != nil {
			t.Errorf("decodeProjectDiscoveryReport(%q) error = %v", output, err)
		}
	}
}

func TestDecodeProjectDiscoveryReportRejectsTrailingJSONAndUnknownFields(t *testing.T) {
	valid := `{"project":{"title":"P","purpose":"P","goal":"P","architecture_summary":"P"},"boundaries":{},"implemented_features":[],"next_tasks":[]}`
	for _, output := range []string{valid + ` {}`, strings.Replace(valid, `"goal":"P"`, `"goal":"P","unknown":true`, 1)} {
		if _, err := decodeProjectDiscoveryReport(output); err == nil {
			t.Errorf("decodeProjectDiscoveryReport accepted %q", output)
		}
	}
}

func TestProjectDiscoverySupportsOnlyNativeReadOnlyProviders(t *testing.T) {
	for provider, want := range map[string]bool{
		"codex": true, "claude": true, "antigravity": true,
		"gemini": false, "cursor": false, "": false,
	} {
		if got := supportsReadOnlyProjectDiscovery(provider); got != want {
			t.Errorf("supportsReadOnlyProjectDiscovery(%q) = %t, want %t", provider, got, want)
		}
	}
}

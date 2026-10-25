package deployment

import (
	"encoding/json"
	"testing"
)

func TestRedactEvidenceRemovesHeadersAndNestedSecrets(t *testing.T) {
	got := RedactEvidence(json.RawMessage(`{"headers":{"Authorization":"Bearer secret","x-request-id":"safe"},"items":[{"token":"secret"}],"message":"safe"}`))
	var value map[string]any
	if err := json.Unmarshal(got, &value); err != nil {
		t.Fatal(err)
	}
	if value["message"] != "safe" || value["headers"].(map[string]any)["x-request-id"] != "safe" {
		t.Fatalf("safe evidence changed: %#v", value)
	}
	if value["headers"].(map[string]any)["Authorization"] != redactedEvidence {
		t.Fatalf("authorization was not redacted: %#v", value)
	}
	if value["items"].([]any)[0].(map[string]any)["token"] != redactedEvidence {
		t.Fatalf("nested token was not redacted: %#v", value)
	}
}

func TestRedactEvidenceTextRemovesRepeatedSecrets(t *testing.T) {
	got := RedactEvidenceText("token=one token=two authorization:three")
	if got != "token=[REDACTED] token=[REDACTED] authorization:[REDACTED]" {
		t.Fatalf("redacted text = %q", got)
	}
}

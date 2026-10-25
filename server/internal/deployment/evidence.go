package deployment

import (
	"bytes"
	"encoding/json"
	"strings"
)

const redactedEvidence = "[REDACTED]"

// RedactEvidence returns JSON safe to persist or expose. It removes secrets
// recursively, including authorization headers and values nested in arrays.
func RedactEvidence(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(`{}`)
	}
	return redactEvidenceValue(value)
}

func redactEvidenceValue(value any) json.RawMessage {
	var redact func(any) any
	redact = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(v))
			for key, item := range v {
				if sensitiveEvidenceKey(key) {
					out[key] = redactedEvidence
				} else {
					out[key] = redact(item)
				}
			}
			return out
		case []any:
			for i := range v {
				v[i] = redact(v[i])
			}
		}
		return value
	}
	b, _ := json.Marshal(redact(value))
	return b
}

func sensitiveEvidenceKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	if strings.Contains(key, "authorization") || strings.Contains(key, "cookie") {
		return true
	}
	for _, name := range SensitiveKeyNames {
		if strings.Contains(key, name) {
			return true
		}
	}
	return false
}

// RedactEvidenceText is used for provider logs that are not JSON.
func RedactEvidenceText(value string) string {
	for _, prefix := range []string{
		"authorization:", "authorization=", "authorization ",
		"bearer:", "bearer ",
		"token=", "token:",
		"access_token=", "access_token:",
		"refresh_token=", "refresh_token:",
		"secret=", "secret:",
		"signing_secret=", "signing_secret:",
		"client_secret=", "client_secret:",
		"api_key=", "api_key:",
		"password=", "password:",
	} {
		searchFrom := 0
		for searchFrom < len(value) {
			lower := strings.ToLower(value)
			relative := strings.Index(lower[searchFrom:], prefix)
			if relative < 0 {
				break
			}
			i := searchFrom + relative
			end := bytes.IndexAny([]byte(value[i+len(prefix):]), " \t\r\n,;)")
			if end < 0 {
				end = len(value) - i - len(prefix)
			}
			value = value[:i+len(prefix)] + redactedEvidence + value[i+len(prefix)+end:]
			searchFrom = i + len(prefix) + len(redactedEvidence)
		}
	}
	return value
}

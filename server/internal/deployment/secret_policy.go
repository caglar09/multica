package deployment

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SensitiveKeyNames lists configuration and payload keys that must never be echoed
// or logged in plaintext.
var SensitiveKeyNames = []string{
	"token",
	"secret",
	"signing_secret",
	"access_token",
	"refresh_token",
	"api_key",
	"password",
	"auth_token",
	"client_secret",
	"bearer",
	"credential",
	"private_key",
	"app_secret",
	"webhook_secret",
}

// CredentialStatus describes the health and validation state of a stored provider credential.
type CredentialStatus string

const (
	CredentialStatusValid   CredentialStatus = "valid"
	CredentialStatusInvalid CredentialStatus = "invalid"
	CredentialStatusRevoked CredentialStatus = "revoked"
	CredentialStatusUnknown CredentialStatus = "unknown"
)

// ProviderConnectionSummary is the write-only response model for provider connection APIs.
// Under no circumstance does this struct include the raw secret, token, or plaintext keys.
type ProviderConnectionSummary struct {
	ID               string           `json:"id"`
	WorkspaceID      string           `json:"workspace_id"`
	Provider         string           `json:"provider"`
	Name             string           `json:"name"`
	Enabled          bool             `json:"enabled"`
	HasCredentials   bool             `json:"has_credentials"`
	CredentialHint   string           `json:"credential_hint,omitempty"`
	CredentialStatus CredentialStatus `json:"credential_status"`
	LastValidatedAt  *time.Time       `json:"last_validated_at,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

// MaskToken converts a sensitive secret or token into a safe, non-sensitive hint.
// Example: "vercel_tok_abcdef123456789" -> "vercel_tok_...6789".
// Shorter tokens retain only the last 3 characters prefixed by "***".
func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	n := len(token)
	if n <= 6 {
		return "***"
	}
	if n <= 12 {
		return "***" + token[n-3:]
	}

	// Keep the recognizable provider prefix, but expose only two characters for
	// generic secrets such as numeric API keys.
	prefixLen := 2
	if strings.HasPrefix(token, "vercel_tok_") || strings.HasPrefix(token, "ghp_") {
		prefixLen = 10
	}
	if prefixLen > n-6 {
		prefixLen = n - 6
	}
	return token[:prefixLen] + "..." + token[n-4:]
}

// ScrubBytes overwrites a byte slice with zeros to minimize the window in which
// decrypted plaintext credentials reside in process memory.
func ScrubBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// SanitizeConfig returns a shallow copy of a key-value map with all known sensitive
// keys replaced with "[REDACTED]". Useful for logging or debugging output.
func SanitizeConfig(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		lower := strings.ToLower(k)
		isSensitive := false
		for _, sens := range SensitiveKeyNames {
			if strings.Contains(lower, sens) {
				isSensitive = true
				break
			}
		}
		if isSensitive {
			out[k] = "[REDACTED]"
		} else {
			out[k] = v
		}
	}
	return out
}

// InspectSecretRef inspects a stored base64-encoded secret reference to verify
// if it contains valid SecretBox key version metadata (0x56 magic prefix + Key ID).
func InspectSecretRef(secretRef string) (keyVersion uint8, isVersioned bool, err error) {
	raw := strings.TrimSpace(secretRef)
	if raw == "" {
		return 0, false, errors.New("empty secret reference")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return 0, false, fmt.Errorf("invalid base64 secret reference: %w", err)
	}
	// Min versioned length: 1 (magic 0x56) + 1 (keyID) + 12 (nonce) + 16 (GCM tag) = 30
	if len(b) >= 30 && b[0] == 0x56 {
		return b[1], true, nil
	}
	return 0, false, nil
}

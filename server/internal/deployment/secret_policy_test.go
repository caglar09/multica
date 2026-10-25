package deployment

import (
	"testing"
)

func TestMaskToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "empty",
			token: "",
			want:  "",
		},
		{
			name:  "short token <= 6",
			token: "secret",
			want:  "***",
		},
		{
			name:  "medium token <= 12",
			token: "my_token_12",
			want:  "***_12",
		},
		{
			name:  "vercel personal access token",
			token: "vercel_tok_1234567890abcdef1234",
			want:  "vercel_tok...1234",
		},
		{
			name:  "generic api key",
			token: "999988887777666655554444",
			want:  "99...4444",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskToken(tc.token)
			if got != tc.want {
				t.Fatalf("MaskToken(%q) = %q; want %q", tc.token, got, tc.want)
			}
		})
	}
}

func TestScrubBytes(t *testing.T) {
	sensitive := []byte("highly_secret_token_data")
	ScrubBytes(sensitive)

	for i, b := range sensitive {
		if b != 0 {
			t.Fatalf("byte at index %d was not scrubbed: got %d, want 0", i, b)
		}
	}
}

func TestSanitizeConfig(t *testing.T) {
	cfg := map[string]any{
		"project_id":     "prj_123",
		"team_slug":      "my-team",
		"token":          "secret_token_val",
		"signing_secret": "my_hmac_secret",
		"api_key":        "top_secret_key",
		"port":           8080,
	}

	sanitized := SanitizeConfig(cfg)

	if sanitized["project_id"] != "prj_123" {
		t.Errorf("project_id altered: %v", sanitized["project_id"])
	}
	if sanitized["token"] != "[REDACTED]" {
		t.Errorf("token not redacted: %v", sanitized["token"])
	}
	if sanitized["signing_secret"] != "[REDACTED]" {
		t.Errorf("signing_secret not redacted: %v", sanitized["signing_secret"])
	}
	if sanitized["api_key"] != "[REDACTED]" {
		t.Errorf("api_key not redacted: %v", sanitized["api_key"])
	}
	if sanitized["port"] != 8080 {
		t.Errorf("port altered: %v", sanitized["port"])
	}
}

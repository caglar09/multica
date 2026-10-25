package deployment

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestVerifyVercelSignature(t *testing.T) {
	secret := "vercel_webhook_secret_xyz123"
	body := []byte(`{"id":"dpl_123","type":"deployment.ready","payload":{"url":"https://app.vercel.app"}}`)

	// Generate valid HMAC-SHA1
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	validSig := hex.EncodeToString(mac.Sum(nil))

	t.Run("valid signature", func(t *testing.T) {
		err := VerifyVercelSignature(body, validSig, secret)
		if err != nil {
			t.Fatalf("expected valid signature, got: %v", err)
		}
	})

	t.Run("tampered body", func(t *testing.T) {
		tampered := []byte(`{"id":"dpl_123","type":"deployment.ready","payload":{"url":"https://evil.com"}}`)
		err := VerifyVercelSignature(tampered, validSig, secret)
		if !errors.Is(err, ErrWebhookSignatureInvalid) {
			t.Fatalf("expected ErrWebhookSignatureInvalid for tampered body, got: %v", err)
		}
	})

	t.Run("tampered signature", func(t *testing.T) {
		err := VerifyVercelSignature(body, validSig+"ab", secret)
		if !errors.Is(err, ErrWebhookSignatureInvalid) {
			t.Fatalf("expected ErrWebhookSignatureInvalid for bad sig, got: %v", err)
		}
	})

	t.Run("missing header", func(t *testing.T) {
		err := VerifyVercelSignature(body, "", secret)
		if !errors.Is(err, ErrWebhookSignatureMissing) {
			t.Fatalf("expected ErrWebhookSignatureMissing, got: %v", err)
		}
	})

	t.Run("empty secret", func(t *testing.T) {
		err := VerifyVercelSignature(body, validSig, "")
		if !errors.Is(err, ErrWebhookSecretEmpty) {
			t.Fatalf("expected ErrWebhookSecretEmpty, got: %v", err)
		}
	})
}

func TestVerifyHubSignature256(t *testing.T) {
	secret := "github_webhook_secret_abc456"
	body := []byte(`{"action":"completed","workflow_run":{"id":9988}}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	validSigHex := hex.EncodeToString(mac.Sum(nil))
	validHeader := "sha256=" + validSigHex

	t.Run("valid with prefix", func(t *testing.T) {
		err := VerifyHubSignature256(body, validHeader, secret)
		if err != nil {
			t.Fatalf("expected valid signature, got: %v", err)
		}
	})

	t.Run("valid without prefix", func(t *testing.T) {
		err := VerifyHubSignature256(body, validSigHex, secret)
		if err != nil {
			t.Fatalf("expected valid signature, got: %v", err)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		err := VerifyHubSignature256(body, "sha256=deadbeefdeadbeef", secret)
		if !errors.Is(err, ErrWebhookSignatureInvalid) {
			t.Fatalf("expected ErrWebhookSignatureInvalid, got: %v", err)
		}
	})
}

func TestVerifyTimestampedWebhook(t *testing.T) {
	secret := "stripe_style_secret_789"
	body := []byte(`{"event":"deployment.promoted"}`)
	now := time.Now()
	nowUnix := now.Unix()
	tsStr := fmt.Sprintf("%d", nowUnix)

	payloadToSign := []byte(tsStr + "." + string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payloadToSign)
	validSig := hex.EncodeToString(mac.Sum(nil))

	t.Run("valid within window", func(t *testing.T) {
		err := VerifyTimestampedWebhook(body, validSig, tsStr, secret, now, 5*time.Minute)
		if err != nil {
			t.Fatalf("expected valid timestamped signature, got: %v", err)
		}
	})

	t.Run("expired timestamp beyond tolerance", func(t *testing.T) {
		oldTime := now.Add(-10 * time.Minute)
		oldTsStr := fmt.Sprintf("%d", oldTime.Unix())
		err := VerifyTimestampedWebhook(body, validSig, oldTsStr, secret, now, 5*time.Minute)
		if !errors.Is(err, ErrWebhookTimestampExpired) {
			t.Fatalf("expected ErrWebhookTimestampExpired, got: %v", err)
		}
	})

	t.Run("malformed timestamp", func(t *testing.T) {
		err := VerifyTimestampedWebhook(body, validSig, "not-a-number", secret, now, 5*time.Minute)
		if !errors.Is(err, ErrWebhookTimestampMalformed) {
			t.Fatalf("expected ErrWebhookTimestampMalformed, got: %v", err)
		}
	})
}

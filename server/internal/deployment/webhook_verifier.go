package deployment

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrWebhookSignatureMissing   = errors.New("deployment: webhook signature header missing")
	ErrWebhookSignatureInvalid   = errors.New("deployment: webhook signature invalid")
	ErrWebhookSecretEmpty        = errors.New("deployment: webhook secret not configured")
	ErrWebhookTimestampMissing   = errors.New("deployment: webhook timestamp missing")
	ErrWebhookTimestampExpired   = errors.New("deployment: webhook timestamp outside valid tolerance window")
	ErrWebhookTimestampMalformed = errors.New("deployment: webhook timestamp malformed")
)

// VerifyVercelSignature validates a Vercel webhook signature against the raw request body.
// Vercel delivers signatures in the `x-vercel-signature` header computed as HMAC-SHA1(rawBody, secret).
// The comparison is strictly constant-time via hmac.Equal.
func VerifyVercelSignature(rawBody []byte, headerSig string, secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ErrWebhookSecretEmpty
	}
	headerSig = strings.TrimSpace(headerSig)
	if headerSig == "" {
		return ErrWebhookSignatureMissing
	}

	want, err := hex.DecodeString(headerSig)
	if err != nil {
		return ErrWebhookSignatureInvalid
	}

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(rawBody)
	calculated := mac.Sum(nil)

	if !hmac.Equal(calculated, want) {
		return ErrWebhookSignatureInvalid
	}
	return nil
}

// VerifyHubSignature256 validates GitHub-style or standard HMAC-SHA256 webhook signatures
// delivered in headers such as `X-Hub-Signature-256`.
// Expected format: "sha256=<hex>" (or pure hex if no prefix).
// The comparison is strictly constant-time via hmac.Equal.
func VerifyHubSignature256(rawBody []byte, headerSig string, secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ErrWebhookSecretEmpty
	}
	headerSig = strings.TrimSpace(headerSig)
	if headerSig == "" {
		return ErrWebhookSignatureMissing
	}

	sigHex := strings.TrimPrefix(headerSig, "sha256=")
	want, err := hex.DecodeString(sigHex)
	if err != nil {
		return ErrWebhookSignatureInvalid
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	calculated := mac.Sum(nil)

	if !hmac.Equal(calculated, want) {
		return ErrWebhookSignatureInvalid
	}
	return nil
}

// VerifyTimestampedWebhook validates an HMAC-SHA256 signature alongside an explicit timestamp
// header to guard against replay attacks. The timestamp must fall within [now - maxTolerance, now + maxTolerance].
func VerifyTimestampedWebhook(rawBody []byte, headerSig string, timestampHeader string, secret string, now time.Time, maxTolerance time.Duration) error {
	timestampHeader = strings.TrimSpace(timestampHeader)
	if timestampHeader == "" {
		return ErrWebhookTimestampMissing
	}

	tsInt, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWebhookTimestampMalformed, err)
	}

	eventTime := time.Unix(tsInt, 0)
	diff := now.Sub(eventTime)
	if diff < 0 {
		diff = -diff
	}
	if diff > maxTolerance {
		return ErrWebhookTimestampExpired
	}

	// Message payload for signing: timestamp + "." + rawBody
	payloadToSign := make([]byte, len(timestampHeader)+1+len(rawBody))
	copy(payloadToSign, timestampHeader)
	payloadToSign[len(timestampHeader)] = '.'
	copy(payloadToSign[len(timestampHeader)+1:], rawBody)

	return VerifyHubSignature256(payloadToSign, headerSig, secret)
}

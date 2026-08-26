package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestAuthenticate_Bearer(t *testing.T) {
	auth := config.WebhookAuth{Type: "bearer", Header: "Authorization"}
	body := []byte(`{}`)

	t.Run("exact match with Bearer prefix", func(t *testing.T) {
		hdrs := map[string]string{"authorization": "Bearer s3cret"}
		if err := Authenticate(auth, "s3cret", hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("bare token without prefix", func(t *testing.T) {
		hdrs := map[string]string{"authorization": "s3cret"}
		if err := Authenticate(auth, "s3cret", hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		hdrs := map[string]string{"authorization": "Bearer nope"}
		if !errors.Is(Authenticate(auth, "s3cret", hdrs, body), ErrUnauthorized) {
			t.Fatal("want ErrUnauthorized")
		}
	})

	t.Run("missing header", func(t *testing.T) {
		if !errors.Is(Authenticate(auth, "s3cret", map[string]string{}, body), ErrUnauthorized) {
			t.Fatal("want ErrUnauthorized")
		}
	})

	// The critical negative: an empty configured secret must NEVER authenticate.
	// A naive constant-time compare of "" vs "" succeeds, which would leave the
	// endpoint wide open whenever SSM returned an empty parameter.
	// Test: a header that trims to empty (e.g. "Bearer ") with an empty secret.
	// Only the guard-less compare of "" against "" would pass this.
	t.Run("empty configured secret fails closed", func(t *testing.T) {
		hdrs := map[string]string{"authorization": "Bearer "}
		if !errors.Is(Authenticate(auth, "", hdrs, body), ErrUnauthorized) {
			t.Fatal("empty secret must fail closed")
		}
	})

	t.Run("custom header name", func(t *testing.T) {
		a := config.WebhookAuth{Type: "bearer", Header: "X-Km-Token"}
		hdrs := map[string]string{"x-km-token": "s3cret"}
		if err := Authenticate(a, "s3cret", hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
}

func TestAuthenticate_HMAC(t *testing.T) {
	body := []byte(`{"a":1}`)
	secret := "key"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	auth := config.WebhookAuth{Type: "hmac", Header: "X-Signature"}

	t.Run("valid signature", func(t *testing.T) {
		hdrs := map[string]string{"x-signature": sig}
		if err := Authenticate(auth, secret, hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("sha256= prefix accepted", func(t *testing.T) {
		hdrs := map[string]string{"x-signature": "sha256=" + sig}
		if err := Authenticate(auth, secret, hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("tampered body", func(t *testing.T) {
		hdrs := map[string]string{"x-signature": sig}
		if !errors.Is(Authenticate(auth, secret, hdrs, []byte(`{"a":2}`)), ErrUnauthorized) {
			t.Fatal("want ErrUnauthorized")
		}
	})

	t.Run("empty secret fails closed", func(t *testing.T) {
		// Compute a signature using the empty key, as an attacker would forge.
		// Only the guard-less compare of "" against "" would pass this.
		emptyKeyMac := hmac.New(sha256.New, []byte(""))
		emptyKeyMac.Write(body)
		forged := hex.EncodeToString(emptyKeyMac.Sum(nil))
		hdrs := map[string]string{"x-signature": forged}
		if !errors.Is(Authenticate(auth, "", hdrs, body), ErrUnauthorized) {
			t.Fatal("empty secret must fail closed against a forged empty-key signature")
		}
	})
}

func TestAuthenticate_UnknownTypeFailsClosed(t *testing.T) {
	auth := config.WebhookAuth{Type: "magic", Header: "Authorization"}
	hdrs := map[string]string{"authorization": "anything"}
	if !errors.Is(Authenticate(auth, "s", hdrs, []byte(`{}`)), ErrUnauthorized) {
		t.Fatal("unknown auth type must fail closed")
	}
}

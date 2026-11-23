package mailer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
)

func TestBrevoSendsVerificationEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/smtp/email" {
			t.Errorf("request = %s %s, want POST /v3/smtp/email", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("api-key"); got != "test-api-key" {
			t.Errorf("api-key header = %q, want test key", got)
		}
		if got := r.Header.Get("content-type"); got != "application/json" {
			t.Errorf("content-type = %q, want application/json", got)
		}

		var payload struct {
			Sender struct {
				Email string `json:"email"`
				Name  string `json:"name"`
			} `json:"sender"`
			To []struct {
				Email string `json:"email"`
			} `json:"to"`
			Subject     string `json:"subject"`
			TextContent string `json:"textContent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if payload.Sender.Email != "finance@example.com" || payload.Sender.Name != "Finance" {
			t.Errorf("sender = %+v, want Finance <finance@example.com>", payload.Sender)
		}
		if len(payload.To) != 1 || payload.To[0].Email != "person@example.com" {
			t.Errorf("recipients = %+v, want person@example.com", payload.To)
		}
		if payload.Subject != "Verify your Ledgerly email" ||
			!strings.Contains(payload.TextContent, "https://finance.example.com/#verify_email=token") {
			t.Errorf("unexpected email content: subject=%q text=%q", payload.Subject, payload.TextContent)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	brevo := &Brevo{
		apiKey:   "test-api-key",
		from:     &mail.Address{Name: "Finance", Address: "finance@example.com"},
		client:   server.Client(),
		endpoint: server.URL + "/v3/smtp/email",
	}
	if err := brevo.SendEmailVerification(
		context.Background(),
		"person@example.com",
		"https://finance.example.com/#verify_email=token",
	); err != nil {
		t.Fatalf("SendEmailVerification: %v", err)
	}
}

func TestBrevoRejectsInvalidRecipientAndAPIResponses(t *testing.T) {
	brevo := &Brevo{
		apiKey: "test-api-key",
		from:   &mail.Address{Address: "finance@example.com"},
	}
	if err := brevo.send(context.Background(), "invalid", "subject", "body"); err == nil {
		t.Fatal("send accepted an invalid recipient")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"code":"unauthorized","message":"Key not found"}`)
	}))
	defer server.Close()
	brevo.client = server.Client()
	brevo.endpoint = server.URL
	err := brevo.send(context.Background(), "person@example.com", "subject", "body")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401 (unauthorized): Key not found") {
		t.Fatalf("send error = %v, want detailed HTTP 401 error", err)
	}
}

func TestNewBrevoValidatesConfiguration(t *testing.T) {
	if _, err := NewBrevo("", "finance@example.com"); err == nil {
		t.Fatal("NewBrevo accepted a missing API key")
	}
	if _, err := NewBrevo("test-api-key", "invalid sender"); err == nil {
		t.Fatal("NewBrevo accepted an invalid sender")
	}
	if _, err := NewBrevo("test-api-key", "finance@example.com"); err != nil {
		t.Fatalf("NewBrevo with valid configuration: %v", err)
	}
}

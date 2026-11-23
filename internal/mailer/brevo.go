package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

const brevoEmailEndpoint = "https://api.brevo.com/v3/smtp/email"

type Brevo struct {
	apiKey   string
	from     *mail.Address
	client   *http.Client
	endpoint string
}

func NewBrevo(apiKey, from string) (*Brevo, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("Brevo API key is required")
	}
	if strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("mail sender address is invalid")
	}
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return nil, errors.New("mail sender address is invalid")
	}
	return &Brevo{
		apiKey:   apiKey,
		from:     sender,
		client:   &http.Client{Timeout: 10 * time.Second},
		endpoint: brevoEmailEndpoint,
	}, nil
}

func (b *Brevo) SendPasswordReset(ctx context.Context, recipient, resetURL string) error {
	return b.send(ctx, recipient, "Reset your Ledgerly password",
		"Use this one-time link to reset your Ledgerly password. It expires in 30 minutes:\n\n"+
			resetURL+"\n\nIf you did not request this, you can ignore this email.\n")
}

func (b *Brevo) SendEmailVerification(ctx context.Context, recipient, verificationURL string) error {
	return b.send(ctx, recipient, "Verify your Ledgerly email",
		"Welcome to Ledgerly. Verify your email address using this one-time link. It expires in 24 hours:\n\n"+
			verificationURL+"\n\nIf you did not create this account, you can ignore this email.\n")
}

func (b *Brevo) send(ctx context.Context, recipient, subject, body string) error {
	to, err := mail.ParseAddress(recipient)
	if err != nil || strings.ContainsAny(recipient, "\r\n") {
		return errors.New("email recipient address is invalid")
	}
	message := struct {
		Sender struct {
			Email string `json:"email"`
			Name  string `json:"name,omitempty"`
		} `json:"sender"`
		To []struct {
			Email string `json:"email"`
			Name  string `json:"name,omitempty"`
		} `json:"to"`
		Subject     string `json:"subject"`
		TextContent string `json:"textContent"`
	}{}
	message.Sender.Email = b.from.Address
	message.Sender.Name = b.from.Name
	message.To = append(message.To, struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}{Email: to.Address, Name: to.Name})
	message.Subject = subject
	message.TextContent = body

	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(message); err != nil {
		return fmt.Errorf("encode Brevo email request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, &payload)
	if err != nil {
		return fmt.Errorf("create Brevo email request: %w", err)
	}
	request.Header.Set("api-key", b.apiKey)
	request.Header.Set("accept", "application/json")
	request.Header.Set("content-type", "application/json")

	response, err := b.client.Do(request)
	if err != nil {
		return fmt.Errorf("send email via Brevo: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var providerError struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&providerError); err == nil &&
			providerError.Message != "" {
			if providerError.Code != "" {
				return fmt.Errorf("Brevo email API returned HTTP %d (%s): %s",
					response.StatusCode, providerError.Code, providerError.Message)
			}
			return fmt.Errorf("Brevo email API returned HTTP %d: %s",
				response.StatusCode, providerError.Message)
		}
		return fmt.Errorf("Brevo email API returned HTTP %d", response.StatusCode)
	}
	return nil
}

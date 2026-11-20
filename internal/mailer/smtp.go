package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTP struct {
	host     string
	port     int
	username string
	password string
	from     *mail.Address
}

func NewSMTP(host string, port int, username, password, from string) (*SMTP, error) {
	if strings.TrimSpace(host) == "" || port < 1 || port > 65535 ||
		username == "" || password == "" {
		return nil, errors.New("SMTP host, valid port, username, and password are required")
	}
	if strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("SMTP sender address is invalid")
	}
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return nil, errors.New("SMTP sender address is invalid")
	}
	return &SMTP{host: host, port: port, username: username, password: password, from: sender}, nil
}

func (s *SMTP) SendPasswordReset(ctx context.Context, recipient, resetURL string) error {
	return s.send(ctx, recipient, "Reset your Ledgerly password",
		"Use this one-time link to reset your Ledgerly password. It expires in 30 minutes:\r\n\r\n"+
			resetURL+"\r\n\r\nIf you did not request this, you can ignore this email.\r\n")
}

func (s *SMTP) SendEmailVerification(ctx context.Context, recipient, verificationURL string) error {
	return s.send(ctx, recipient, "Verify your Ledgerly email",
		"Welcome to Ledgerly. Verify your email address using this one-time link. It expires in 24 hours:\r\n\r\n"+
			verificationURL+"\r\n\r\nIf you did not create this account, you can ignore this email.\r\n")
}

func (s *SMTP) send(ctx context.Context, recipient, subject, body string) error {
	to, err := mail.ParseAddress(recipient)
	if err != nil || strings.ContainsAny(recipient, "\r\n") {
		return errors.New("email recipient address is invalid")
	}
	address := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set SMTP deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("start SMTP session: %w", err)
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return errors.New("SMTP server does not offer STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
		return fmt.Errorf("start SMTP TLS: %w", err)
	}
	if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
		return fmt.Errorf("authenticate with SMTP server: %w", err)
	}
	if err := client.Mail(s.from.Address); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("start SMTP message: %w", err)
	}
	message := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.from.String(), to.String(), subject, body,
	)
	if _, err := io.WriteString(writer, message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("send SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("close SMTP session: %w", err)
	}
	return nil
}

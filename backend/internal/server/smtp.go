package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SMTPTestInput struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	TLS       string `json:"tls"`
	From      string `json:"from"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Recipient string `json:"recipient"`
}

type SMTPMailer struct {
	Settings SMTPSettings
	Password string
}

func (m SMTPMailer) Send(ctx context.Context, message MailMessage) error {
	subject, body, err := accountMailContent(message)
	if err != nil {
		return err
	}
	return sendSMTP(ctx, SMTPTestInput{
		Host: m.Settings.Host, Port: m.Settings.Port, TLS: m.Settings.TLS, From: m.Settings.From,
		Username: m.Settings.Username, Password: m.Password, Recipient: message.To,
	}, subject, body)
}

func accountMailContent(message MailMessage) (string, string, error) {
	subject := "KeKeIO Tab 邮箱验证"
	accountPath := "/account/verify"
	instruction := "请打开下面的链接完成邮箱验证："
	if message.Kind == "reset_password" {
		subject = "KeKeIO Tab 密码重置"
		accountPath = "/account/reset"
		instruction = "请打开下面的链接设置新密码："
	} else if message.Kind != "verify_email" {
		return "", "", fmt.Errorf("unknown account mail kind")
	}
	if strings.TrimSpace(message.Token) == "" {
		return "", "", fmt.Errorf("account mail token is required")
	}
	baseURL, err := url.Parse(strings.TrimSpace(message.BaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil {
		return "", "", fmt.Errorf("invalid public base URL")
	}
	baseURL.RawQuery = ""
	baseURL.Fragment = ""
	baseURL.RawFragment = ""
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + accountPath
	baseURL.RawPath = ""
	link := baseURL.String() + "#token=" + url.QueryEscape(message.Token)
	body := fmt.Sprintf("%s\n\n%s\n%s\n\n如果你没有发起此操作，可以忽略本邮件。\n", subject, instruction, link)
	return subject, body, nil
}

func TestSMTP(ctx context.Context, input SMTPTestInput) error {
	return sendSMTP(ctx, input, "KeKeIO Tab SMTP 测试", "KeKeIO Tab 邮件服务配置测试成功。\n")
}

func sendSMTP(ctx context.Context, input SMTPTestInput, subject, body string) error {
	input.Host = strings.TrimSpace(input.Host)
	input.From = normalizeEmail(input.From)
	input.Recipient = normalizeEmail(input.Recipient)
	if input.Host == "" || input.Port < 1 || input.Port > 65535 || !oneOf(input.TLS, "none", "starttls", "tls") || !validEmail(input.From) || !validEmail(input.Recipient) || strings.ContainsAny(subject, "\r\n") {
		return fmt.Errorf("invalid SMTP test settings")
	}
	address := net.JoinHostPort(input.Host, strconv.Itoa(input.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var client *smtp.Client
	if input.TLS == "tls" {
		connection, err := tls.DialWithDialer(dialer, "tcp", address, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: input.Host})
		if err != nil {
			return err
		}
		client, err = smtp.NewClient(connection, input.Host)
		if err != nil {
			_ = connection.Close()
			return err
		}
	} else {
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		client, err = smtp.NewClient(connection, input.Host)
		if err != nil {
			_ = connection.Close()
			return err
		}
		if input.TLS == "starttls" {
			if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: input.Host}); err != nil {
				_ = client.Close()
				return err
			}
		}
	}
	defer client.Close()
	if input.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", input.Username, input.Password, input.Host)); err != nil {
			return err
		}
	}
	from, _ := mail.ParseAddress(input.From)
	recipient, _ := mail.ParseAddress(input.Recipient)
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", from.Address, recipient.Address, subject, body)
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

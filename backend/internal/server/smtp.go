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

const smtpSendTimeout = 25 * time.Second

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

	// TCP 建连后的 greeting、TLS、认证与投递共享同一个总截止时间。
	sendContext, cancel := context.WithTimeout(ctx, smtpSendTimeout)
	defer cancel()

	address := net.JoinHostPort(input.Host, strconv.Itoa(input.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(sendContext, "tcp", address)
	if err != nil {
		return smtpContextError(sendContext, err)
	}
	return sendSMTPConnection(sendContext, connection, input, subject, body)
}

func sendSMTPConnection(sendContext context.Context, connection net.Conn, input SMTPTestInput, subject, body string) error {
	defer connection.Close()

	if deadline, ok := sendContext.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return err
		}
	}
	// net/smtp 的各阶段不接收 context；请求取消时关闭连接以立即解除阻塞。
	stopContextClose := context.AfterFunc(sendContext, func() {
		_ = connection.Close()
	})
	defer stopContextClose()

	smtpConnection := connection
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: input.Host}
	if input.TLS == "tls" {
		tlsConnection := tls.Client(connection, tlsConfig)
		if err := tlsConnection.HandshakeContext(sendContext); err != nil {
			return smtpContextError(sendContext, err)
		}
		smtpConnection = tlsConnection
	}

	client, err := smtp.NewClient(smtpConnection, input.Host)
	if err != nil {
		return smtpContextError(sendContext, err)
	}
	defer client.Close()

	if input.TLS == "starttls" {
		if err := client.StartTLS(tlsConfig); err != nil {
			return smtpContextError(sendContext, err)
		}
	}
	if input.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", input.Username, input.Password, input.Host)); err != nil {
			return smtpContextError(sendContext, err)
		}
	}
	from, _ := mail.ParseAddress(input.From)
	recipient, _ := mail.ParseAddress(input.Recipient)
	if err := client.Mail(from.Address); err != nil {
		return smtpContextError(sendContext, err)
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return smtpContextError(sendContext, err)
	}
	writer, err := client.Data()
	if err != nil {
		return smtpContextError(sendContext, err)
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", from.Address, recipient.Address, subject, body)
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return smtpContextError(sendContext, err)
	}
	if err := writer.Close(); err != nil {
		return smtpContextError(sendContext, err)
	}
	return nil
}

func smtpContextError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	return err
}

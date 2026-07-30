package server

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type RuntimeOverrides struct {
	PublicBaseURL    *string
	RegistrationOpen *bool
	AllowedOrigins   *[]string
}

func ApplyRuntimeSettings(config Config, settings RuntimeSettings, secrets Secrets, overrides RuntimeOverrides) Config {
	config.PublicBaseURL = settings.PublicBaseURL
	config.RegistrationOpen = settings.RegistrationOpen
	config.AllowedOrigins = append([]string(nil), settings.AllowedOrigins...)
	if len(config.TokenDerivationKey) == 0 {
		config.TokenDerivationKey = append([]byte(nil), secrets.TokenDerivationKey...)
	}
	if settings.SMTP != nil {
		config.Mailer = runtimeSMTPMailer(settings.SMTP, secrets.SMTPPassword)
	}
	if overrides.PublicBaseURL != nil {
		config.PublicBaseURL = *overrides.PublicBaseURL
	}
	if overrides.RegistrationOpen != nil {
		config.RegistrationOpen = *overrides.RegistrationOpen
	}
	if overrides.AllowedOrigins != nil {
		config.AllowedOrigins = append([]string(nil), (*overrides.AllowedOrigins)...)
	}
	return config
}

func runtimeSMTPMailer(settings *SMTPSettings, password string) Mailer {
	if settings == nil {
		return nil
	}
	normalized, err := normalizeSMTPVerificationInput(SMTPTestInput{
		Host: settings.Host, Port: settings.Port, TLS: settings.TLS, From: settings.From,
		Username: settings.Username, Password: password,
	})
	if err != nil || (normalized.Username != "" && password == "") {
		return nil
	}
	return SMTPMailer{
		Settings: SMTPSettings{
			Host: normalized.Host, Port: normalized.Port, TLS: normalized.TLS,
			From: normalized.From, Username: normalized.Username,
		},
		Password: password,
	}
}

func ValidateConfig(config Config) error {
	if config.PasswordHashConcurrency < 0 || config.PasswordHashConcurrency > maxPasswordHashConcurrency {
		return fmt.Errorf("password hash concurrency must be between 1 and %d", maxPasswordHashConcurrency)
	}
	if !config.DevelopmentMode && len(config.AdminAllowedCIDRs) == 0 {
		return fmt.Errorf("admin allowed CIDR is required outside development mode")
	}
	for label, values := range map[string][]string{
		"admin allowed CIDR": config.AdminAllowedCIDRs,
		"trusted proxy CIDR": config.TrustedProxyCIDRs,
	} {
		for _, raw := range values {
			value := strings.TrimSpace(raw)
			if value == "" {
				return fmt.Errorf("%s contains an empty value", label)
			}
			if _, err := netip.ParsePrefix(value); err == nil {
				continue
			}
			if _, err := netip.ParseAddr(value); err == nil {
				continue
			}
			return fmt.Errorf("invalid %s %q", label, value)
		}
	}
	return nil
}

func NewHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

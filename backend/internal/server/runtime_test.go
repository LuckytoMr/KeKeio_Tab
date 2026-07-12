package server

import (
	"net/http"
	"testing"
)

func TestNewHTTPServerSetsDefensiveTimeouts(t *testing.T) {
	httpServer := NewHTTPServer(":0", http.NewServeMux())
	if httpServer.ReadHeaderTimeout <= 0 || httpServer.ReadTimeout <= 0 || httpServer.WriteTimeout <= 0 || httpServer.IdleTimeout <= 0 {
		t.Fatalf("server timeouts are incomplete: %#v", httpServer)
	}
	if httpServer.MaxHeaderBytes < 8<<10 || httpServer.MaxHeaderBytes > 64<<10 {
		t.Fatalf("max header bytes = %d", httpServer.MaxHeaderBytes)
	}
}

func TestValidateConfigRejectsInvalidStartupCIDRs(t *testing.T) {
	for name, config := range map[string]Config{
		"admin":   {AdminAllowedCIDRs: []string{"192.168.0.0/16", "not-a-cidr"}},
		"trusted": {TrustedProxyCIDRs: []string{"10.0.0.0/99"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateConfig(config); err == nil {
				t.Fatalf("invalid %s network configuration was accepted", name)
			}
		})
	}
	if err := ValidateConfig(Config{
		AdminAllowedCIDRs: []string{"127.0.0.1", "192.168.0.0/16", "fd00::/8"},
		TrustedProxyCIDRs: []string{"127.0.0.1/32", "::1"},
	}); err != nil {
		t.Fatalf("valid startup CIDRs rejected: %v", err)
	}
}

func TestValidateConfigRejectsUnsafePasswordHashConcurrency(t *testing.T) {
	for _, value := range []int{-1, maxPasswordHashConcurrency + 1} {
		if err := ValidateConfig(Config{PasswordHashConcurrency: value}); err == nil {
			t.Fatalf("password hash concurrency %d was accepted", value)
		}
	}
	for _, value := range []int{0, 1, defaultPasswordHashConcurrency, maxPasswordHashConcurrency} {
		if err := ValidateConfig(Config{PasswordHashConcurrency: value}); err != nil {
			t.Fatalf("password hash concurrency %d rejected: %v", value, err)
		}
	}
}

func TestNewAppConfiguresProcessWidePasswordHashLimit(t *testing.T) {
	defer configurePasswordHashConcurrency(defaultPasswordHashConcurrency)
	NewApp(nil, Config{PasswordHashConcurrency: 1})
	if got := cap(currentPasswordHashLimiter().slots); got != 1 {
		t.Fatalf("password hash concurrency = %d, want 1", got)
	}
}

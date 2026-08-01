package server

import (
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type uhdpaperRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn uhdpaperRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newUHDpaperProxyTestHandler(t *testing.T, transport http.RoundTripper) (http.Handler, string) {
	t.Helper()
	_, handler, access := newUHDpaperProxyTestApp(t, transport)
	return handler, access
}

func newUHDpaperProxyTestApp(t *testing.T, transport http.RoundTripper) (*App, http.Handler, string) {
	t.Helper()
	store := newTestStore(t)
	if _, err := store.BeginInstallation(t.Context(), InstallationInput{
		Mode: "fresh_install", Email: "owner@example.com", DisplayName: "Owner",
		Password: "correct horse battery staple", ExternalBaseURL: "https://fullpro.example",
		AllowedOrigins: []string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop"},
	}); err != nil {
		t.Fatalf("begin installation: %v", err)
	}
	if err := store.FinishInstallation(t.Context()); err != nil {
		t.Fatalf("finish installation: %v", err)
	}
	mailer := &captureMailer{}
	app := NewApp(store, Config{
		CookieName:         "fullpro_test_session",
		Mailer:             mailer,
		PublicBaseURL:      "https://fullpro.example",
		TokenDerivationKey: []byte("0123456789abcdef0123456789abcdef"),
		RegistrationOpen:   true,
		AllowedOrigins:     []string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop"},
		AdminAllowedCIDRs:  []string{"127.0.0.1/32"},
		UHDpaperTransport:  transport,
	})
	handler := app.Routes()
	access := verifiedAccessToken(t, handler, mailer, "uhdpaper@example.com", "uhdpaper-device")
	return app, handler, access
}

func uhdpaperResponse(request *http.Request, status int, contentType string, body io.Reader) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(body),
		Request:    request,
	}
}

func TestUHDpaperProxyRequiresPluginAuthentication(t *testing.T) {
	var calls atomic.Int32
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return uhdpaperResponse(request, http.StatusOK, "text/html", strings.NewReader("ok")), nil
	})
	handler, _ := newUHDpaperProxyTestHandler(t, transport)

	for _, path := range []string{
		"/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%2F",
		"/api/v1/catalog/uhdpaper/image?url=https%3A%2F%2Fcdn.uhdpaper.com%2Fwallpaper%2Fsample.jpg",
	} {
		response := getV1(t, handler, path, "")
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"UNAUTHORIZED"`) {
			t.Fatalf("anonymous %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("unauthorized requests reached upstream transport: %d", calls.Load())
	}
}

func TestUHDpaperPageProxyReturnsAuthenticatedEnvelope(t *testing.T) {
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://www.uhdpaper.com/?page=2" {
			t.Fatalf("upstream URL = %s", request.URL)
		}
		if request.Header.Get("Referer") != uhdpaperReferer || request.Header.Get("User-Agent") != uhdpaperUserAgent {
			t.Fatalf("upstream headers = %#v", request.Header)
		}
		return uhdpaperResponse(request, http.StatusOK, "text/html; charset=utf-8", strings.NewReader("<main>wallpapers</main>")), nil
	})
	handler, access := newUHDpaperProxyTestHandler(t, transport)

	response := getV1(t, handler, "/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%2F%3Fpage%3D2", access)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"data":{"html":"\u003cmain\u003ewallpapers\u003c/main\u003e"}`) {
		t.Fatalf("page proxy = %d %s", response.Code, response.Body.String())
	}
}

func TestUHDpaperImageProxyReturnsValidatedDataURL(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x01, 0x02, 0x03}
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return uhdpaperResponse(request, http.StatusOK, "image/jpeg", strings.NewReader(string(jpeg))), nil
	})
	handler, access := newUHDpaperProxyTestHandler(t, transport)

	response := getV1(t, handler, "/api/v1/catalog/uhdpaper/image?url=https%3A%2F%2Fcdn.uhdpaper.com%2Fwallpaper%2Fsample.jpg", access)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mimeType":"image/jpeg"`) ||
		!strings.Contains(response.Body.String(), `"dataUrl":"data:image/jpeg;base64,`) {
		t.Fatalf("image proxy = %d %s", response.Code, response.Body.String())
	}
}

func TestUHDpaperProxyRejectsNonAllowlistedURLsBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return uhdpaperResponse(request, http.StatusOK, "text/html", strings.NewReader("unexpected")), nil
	})
	handler, access := newUHDpaperProxyTestHandler(t, transport)

	paths := []string{
		"/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fevil.example%2F",
		"/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%3A8443%2F",
		"/api/v1/catalog/uhdpaper/image?url=https%3A%2F%2Fuhdpaper.com.evil.example%2Fwallpaper%2Fx.jpg",
		"/api/v1/catalog/uhdpaper/image?url=https%3A%2F%2Fcdn.uhdpaper.com%2Fassets%2Fx.jpg",
	}
	for _, path := range paths {
		response := getV1(t, handler, path, access)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"INVALID_UHDPAPER_URL"`) {
			t.Fatalf("invalid URL %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid URLs reached upstream transport: %d", calls.Load())
	}
}

func TestUHDpaperImagePathCanonicalizationRejectsTraversalBypasses(t *testing.T) {
	for _, rawURL := range []string{
		"https://cdn.uhdpaper.com/wallpaper/../private.jpg",
		"https://cdn.uhdpaper.com/wallpaper/%2e%2e/private.jpg",
		"https://cdn.uhdpaper.com/wallpaper/%2E%2E%2Fprivate.jpg",
		"https://cdn.uhdpaper.com/wallpaper/%5c..%5c..%5cprivate.jpg",
		"https://cdn.uhdpaper.com/wallpaper/%252e%252e%252fprivate.jpg",
		"https://cdn.uhdpaper.com/wallpaper/%25252e%25252e%25252fprivate.jpg",
		"https://cdn.uhdpaper.com/wallpaper/%25255c%25252e%25252e%25255cprivate.jpg",
	} {
		if target, err := validateUHDpaperURL(rawURL, uhdpaperImageResource); err == nil {
			t.Errorf("traversal URL unexpectedly allowed as %s: %s", target, rawURL)
		}
	}
}

func TestUHDpaperImagePathCanonicalizationRewritesSafeEncodedPaths(t *testing.T) {
	target, err := validateUHDpaperURL(
		"https://cdn.uhdpaper.com/wallpaper/folder%252fsub%255c.%255cimage%252ejpg?quality=90",
		uhdpaperImageResource,
	)
	if err != nil {
		t.Fatalf("safe encoded path rejected: %v", err)
	}
	if target.Path != "/wallpaper/folder/sub/image.jpg" || target.RawPath != "" || target.RawQuery != "quality=90" {
		t.Fatalf("canonical target = %#v", target)
	}
}

func TestUHDpaperProxyRevalidatesRedirectTargets(t *testing.T) {
	var calls atomic.Int32
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		response := uhdpaperResponse(request, http.StatusFound, "text/html", strings.NewReader("redirect"))
		response.Header.Set("Location", "https://evil.example/wallpaper/escape.jpg")
		return response, nil
	})
	handler, access := newUHDpaperProxyTestHandler(t, transport)

	response := getV1(t, handler, "/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%2Fredirect", access)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"code":"UHDPAPER_UNAVAILABLE"`) {
		t.Fatalf("invalid redirect = %d %s", response.Code, response.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("redirect target reached transport; calls = %d", calls.Load())
	}
}

func TestUHDpaperImageProxyRejectsNonImagePayload(t *testing.T) {
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return uhdpaperResponse(request, http.StatusOK, "text/html; charset=utf-8", strings.NewReader("<script>alert(1)</script>")), nil
	})
	handler, access := newUHDpaperProxyTestHandler(t, transport)

	response := getV1(t, handler, "/api/v1/catalog/uhdpaper/image?url=https%3A%2F%2Fcdn.uhdpaper.com%2Fwallpaper%2Fnot-an-image.jpg", access)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"code":"UHDPAPER_INVALID_IMAGE"`) {
		t.Fatalf("non-image = %d %s", response.Code, response.Body.String())
	}
}

func TestUHDpaperPageProxyRejectsNonHTMLPayload(t *testing.T) {
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return uhdpaperResponse(request, http.StatusOK, "application/json", strings.NewReader(`{"unexpected":true}`)), nil
	})
	handler, access := newUHDpaperProxyTestHandler(t, transport)

	response := getV1(t, handler, "/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%2Fnot-html", access)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"code":"UHDPAPER_INVALID_PAGE"`) {
		t.Fatalf("non-HTML page = %d %s", response.Code, response.Body.String())
	}
}

func TestUHDpaperProxyRejectsOversizedResponse(t *testing.T) {
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return uhdpaperResponse(request, http.StatusOK, "text/html", strings.NewReader(strings.Repeat("a", int(uhdpaperPageBodyLimit)+1))), nil
	})
	handler, access := newUHDpaperProxyTestHandler(t, transport)

	response := getV1(t, handler, "/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%2Flarge", access)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"code":"UHDPAPER_RESPONSE_TOO_LARGE"`) {
		t.Fatalf("oversized page = %d %s", response.Code, response.Body.String())
	}
}

func TestUHDpaperProxyRateLimitRunsAfterAuthentication(t *testing.T) {
	var calls atomic.Int32
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return uhdpaperResponse(request, http.StatusOK, "text/html", strings.NewReader("<main>ok</main>")), nil
	})
	app, handler, access := newUHDpaperProxyTestApp(t, transport)
	fixedNow := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	app.uhdpaperLimiter.clock = func() time.Time { return fixedNow }
	path := "/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%2F"

	for index := 0; index < uhdpaperIPRateLimit+5; index++ {
		anonymous := getV1(t, handler, path, "")
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous request %d = %d %s", index+1, anonymous.Code, anonymous.Body.String())
		}
	}
	for index := 0; index < uhdpaperIPRateLimit; index++ {
		response := getV1(t, handler, path, access)
		if response.Code != http.StatusOK {
			t.Fatalf("allowed request %d = %d %s", index+1, response.Code, response.Body.String())
		}
	}

	limited := getV1(t, handler, path, access)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "60" ||
		!strings.Contains(limited.Body.String(), `"code":"UHDPAPER_RATE_LIMITED"`) {
		t.Fatalf("rate limited response = %d headers=%v body=%s", limited.Code, limited.Header(), limited.Body.String())
	}
	if calls.Load() != uhdpaperIPRateLimit {
		t.Fatalf("upstream calls = %d, want %d", calls.Load(), uhdpaperIPRateLimit)
	}
}

func TestUHDpaperProxyGlobalConcurrencyLimit(t *testing.T) {
	entered := make(chan struct{}, uhdpaperGlobalConcurrency)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	transport := uhdpaperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-release
		return uhdpaperResponse(request, http.StatusOK, "text/html", strings.NewReader("<main>ok</main>")), nil
	})
	_, handler, access := newUHDpaperProxyTestApp(t, transport)
	path := "/api/v1/catalog/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%2F"
	results := make(chan int, uhdpaperGlobalConcurrency)

	for index := 0; index < uhdpaperGlobalConcurrency; index++ {
		go func() {
			results <- getV1(t, handler, path, access).Code
		}()
	}
	for index := 0; index < uhdpaperGlobalConcurrency; index++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for requests to occupy UHDpaper slots")
		}
	}

	busy := getV1(t, handler, path, access)
	if busy.Code != http.StatusTooManyRequests || busy.Header().Get("Retry-After") != "1" ||
		!strings.Contains(busy.Body.String(), `"code":"UHDPAPER_BUSY"`) {
		t.Fatalf("busy response = %d headers=%v body=%s", busy.Code, busy.Header(), busy.Body.String())
	}
	releaseOnce.Do(func() { close(release) })
	for index := 0; index < uhdpaperGlobalConcurrency; index++ {
		select {
		case status := <-results:
			if status != http.StatusOK {
				t.Errorf("in-flight request status = %d", status)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for in-flight UHDpaper request")
		}
	}
}

func TestUHDpaperDialerAddressPolicyRejectsSSRFNetworks(t *testing.T) {
	for _, value := range []string{
		"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.1.1", "224.0.0.1",
		"::", "::1", "fc00::1", "fe80::1", "ff02::1", "2001:db8::1",
	} {
		address := netip.MustParseAddr(value)
		if isPublicUHDpaperAddress(address) {
			t.Errorf("address %s unexpectedly allowed", address)
		}
	}
	for _, value := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		address := netip.MustParseAddr(value)
		if !isPublicUHDpaperAddress(address) {
			t.Errorf("public address %s unexpectedly rejected", address)
		}
	}
}

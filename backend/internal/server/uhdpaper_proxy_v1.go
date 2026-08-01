package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	pathpkg "path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	uhdpaperPageBodyLimit     int64 = 2 << 20
	uhdpaperImageBodyLimit    int64 = 20 << 20
	uhdpaperRedirectLimit           = 3
	uhdpaperGlobalConcurrency       = 2
	uhdpaperIPRateLimit             = 30
	uhdpaperRateMaxBuckets          = 4096
	uhdpaperRateWindow              = time.Minute
	uhdpaperReferer                 = "https://www.uhdpaper.com/"
	uhdpaperUserAgent               = "Mozilla/5.0 (compatible; kekeio/1.0; +https://kekeio.com)"
)

var errUHDpaperResponseTooLarge = errors.New("uhdpaper response exceeds size limit")

type uhdpaperResourceKind uint8

const (
	uhdpaperPageResource uhdpaperResourceKind = iota + 1
	uhdpaperImageResource
)

type uhdpaperPageResponse struct {
	HTML string `json:"html"`
}

type uhdpaperImageResponse struct {
	MIMEType string `json:"mimeType"`
	DataURL  string `json:"dataUrl"`
}

type uhdpaperFixedWindowLimiter struct {
	limit      int
	window     time.Duration
	maxBuckets int
	clock      func() time.Time
	mu         sync.Mutex
	buckets    map[string]uhdpaperFixedWindowBucket
}

type uhdpaperFixedWindowBucket struct {
	windowStart time.Time
	count       int
}

func newUHDpaperFixedWindowLimiter(limit int, window time.Duration, maxBuckets int) *uhdpaperFixedWindowLimiter {
	return &uhdpaperFixedWindowLimiter{
		limit: limit, window: window, maxBuckets: maxBuckets, clock: time.Now,
		buckets: make(map[string]uhdpaperFixedWindowBucket),
	}
}

func (l *uhdpaperFixedWindowLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	windowStart := now.Truncate(l.window)
	retryAfter := windowStart.Add(l.window).Sub(now)
	if retryAfter <= 0 {
		retryAfter = l.window
	}
	for bucketKey, bucket := range l.buckets {
		if !bucket.windowStart.Equal(windowStart) {
			delete(l.buckets, bucketKey)
		}
	}

	bucket, found := l.buckets[key]
	if !found {
		if len(l.buckets) >= l.maxBuckets {
			return false, retryAfter
		}
		bucket.windowStart = windowStart
	}
	if bucket.count >= l.limit {
		return false, retryAfter
	}
	bucket.count++
	l.buckets[key] = bucket
	return true, 0
}

func (a *App) withUHDpaperRequestLimits(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientKey := "unknown"
		if clientIP, ok := a.clientIP(r); ok {
			clientKey = clientIP.String()
		}
		if allowed, retryAfter := a.uhdpaperLimiter.allow(clientKey); !allowed {
			writeUHDpaperRetryAfter(w, retryAfter)
			writeAPIError(w, http.StatusTooManyRequests, "UHDPAPER_RATE_LIMITED", "UHDpaper 请求过于频繁，请稍后重试")
			return
		}

		select {
		case a.uhdpaperSlots <- struct{}{}:
			defer func() { <-a.uhdpaperSlots }()
			next(w, r)
		default:
			writeUHDpaperRetryAfter(w, time.Second)
			writeAPIError(w, http.StatusTooManyRequests, "UHDPAPER_BUSY", "UHDpaper 代理正忙，请稍后重试")
		}
	}
}

func writeUHDpaperRetryAfter(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int64((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
}

func (a *App) handleUHDpaperPageV1(w http.ResponseWriter, r *http.Request) {
	payload, contentType, err := a.fetchUHDpaper(r.Context(), r.URL.Query().Get("url"), uhdpaperPageResource, uhdpaperPageBodyLimit)
	if err != nil {
		writeUHDpaperProxyError(w, err)
		return
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		writeAPIError(w, http.StatusBadGateway, "UHDPAPER_INVALID_PAGE", "壁纸源返回的内容不是 HTML 页面")
		return
	}
	writeAPIData(w, http.StatusOK, uhdpaperPageResponse{HTML: string(payload)})
}

func (a *App) handleUHDpaperImageV1(w http.ResponseWriter, r *http.Request) {
	payload, contentType, err := a.fetchUHDpaper(r.Context(), r.URL.Query().Get("url"), uhdpaperImageResource, uhdpaperImageBodyLimit)
	if err != nil {
		writeUHDpaperProxyError(w, err)
		return
	}

	headerMIME, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(strings.ToLower(headerMIME), "image/") {
		writeAPIError(w, http.StatusBadGateway, "UHDPAPER_INVALID_IMAGE", "壁纸源返回的内容不是图片")
		return
	}
	sniffedMIME, _, err := mime.ParseMediaType(http.DetectContentType(payload))
	if err != nil || !strings.HasPrefix(strings.ToLower(sniffedMIME), "image/") {
		writeAPIError(w, http.StatusBadGateway, "UHDPAPER_INVALID_IMAGE", "壁纸源返回的内容不是有效图片")
		return
	}

	writeAPIData(w, http.StatusOK, uhdpaperImageResponse{
		MIMEType: sniffedMIME,
		DataURL:  "data:" + sniffedMIME + ";base64," + base64.StdEncoding.EncodeToString(payload),
	})
}

type uhdpaperProxyError struct {
	status  int
	code    string
	message string
	cause   error
}

func (e *uhdpaperProxyError) Error() string {
	if e.cause == nil {
		return e.message
	}
	return e.message + ": " + e.cause.Error()
}

func (e *uhdpaperProxyError) Unwrap() error { return e.cause }

func writeUHDpaperProxyError(w http.ResponseWriter, err error) {
	var proxyErr *uhdpaperProxyError
	if errors.As(err, &proxyErr) {
		writeAPIError(w, proxyErr.status, proxyErr.code, proxyErr.message)
		return
	}
	writeAPIError(w, http.StatusBadGateway, "UHDPAPER_UNAVAILABLE", "暂时无法读取 UHDpaper 资源")
}

func (a *App) fetchUHDpaper(ctx context.Context, rawURL string, kind uhdpaperResourceKind, bodyLimit int64) ([]byte, string, error) {
	target, err := validateUHDpaperURL(rawURL, kind)
	if err != nil {
		return nil, "", &uhdpaperProxyError{
			status: http.StatusBadRequest, code: "INVALID_UHDPAPER_URL", message: "UHDpaper 资源地址不在允许范围内", cause: err,
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, "", &uhdpaperProxyError{
			status: http.StatusBadRequest, code: "INVALID_UHDPAPER_URL", message: "UHDpaper 资源地址无效", cause: err,
		}
	}
	request.Header.Set("Referer", uhdpaperReferer)
	request.Header.Set("User-Agent", uhdpaperUserAgent)
	if kind == uhdpaperPageResource {
		request.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	} else {
		request.Header.Set("Accept", "image/avif,image/webp,image/apng,image/jpeg,image/png,image/*;q=0.8")
	}

	client := &http.Client{
		Transport: a.uhdpaperTransport,
		Timeout:   uhdpaperRequestTimeout(kind),
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) > uhdpaperRedirectLimit {
				return errors.New("too many redirects")
			}
			if _, validationErr := validateUHDpaperURL(next.URL.String(), kind); validationErr != nil {
				return validationErr
			}
			next.Header.Set("Referer", uhdpaperReferer)
			next.Header.Set("User-Agent", uhdpaperUserAgent)
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		status := http.StatusBadGateway
		code := "UHDPAPER_UNAVAILABLE"
		message := "暂时无法读取 UHDpaper 资源"
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
			code = "UHDPAPER_TIMEOUT"
			message = "读取 UHDpaper 资源超时"
		}
		return nil, "", &uhdpaperProxyError{status: status, code: code, message: message, cause: err}
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, "", &uhdpaperProxyError{
			status: http.StatusBadGateway, code: "UHDPAPER_BAD_STATUS", message: "UHDpaper 资源暂时不可用",
			cause: fmt.Errorf("upstream status %d", response.StatusCode),
		}
	}
	payload, err := readUHDpaperBody(response.Body, bodyLimit)
	if err != nil {
		if errors.Is(err, errUHDpaperResponseTooLarge) {
			return nil, "", &uhdpaperProxyError{
				status: http.StatusBadGateway, code: "UHDPAPER_RESPONSE_TOO_LARGE", message: "UHDpaper 资源超过允许大小", cause: err,
			}
		}
		return nil, "", &uhdpaperProxyError{
			status: http.StatusBadGateway, code: "UHDPAPER_READ_FAILED", message: "读取 UHDpaper 资源失败", cause: err,
		}
	}
	return payload, response.Header.Get("Content-Type"), nil
}

func readUHDpaperBody(body io.Reader, limit int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, errUHDpaperResponseTooLarge
	}
	return payload, nil
}

func uhdpaperRequestTimeout(kind uhdpaperResourceKind) time.Duration {
	if kind == uhdpaperImageResource {
		return 30 * time.Second
	}
	return 20 * time.Second
}

func validateUHDpaperURL(rawURL string, kind uhdpaperResourceKind) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if target.Scheme != "https" || target.Host == "" || target.User != nil || target.Opaque != "" {
		return nil, errors.New("only absolute HTTPS URLs without user info are allowed")
	}
	if port := target.Port(); port != "" && port != "443" {
		return nil, errors.New("only the default HTTPS port is allowed")
	}
	hostname := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	switch kind {
	case uhdpaperPageResource:
		if hostname != "www.uhdpaper.com" {
			return nil, errors.New("page host is not allowed")
		}
	case uhdpaperImageResource:
		if hostname != "uhdpaper.com" && !strings.HasSuffix(hostname, ".uhdpaper.com") {
			return nil, errors.New("image host is not allowed")
		}
		canonicalPath, pathErr := canonicalUHDpaperImagePath(target.EscapedPath())
		if pathErr != nil {
			return nil, pathErr
		}
		target.Path = canonicalPath
		target.RawPath = ""
	default:
		return nil, errors.New("unsupported resource kind")
	}
	target.Fragment = ""
	return target, nil
}

func canonicalUHDpaperImagePath(escapedPath string) (string, error) {
	decoded := escapedPath
	for step := 0; hasPercentEncodedOctet(decoded); step++ {
		if step > len(escapedPath) {
			return "", errors.New("image path encoding is too deeply nested")
		}
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return "", errors.New("image path encoding is invalid")
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	decoded = strings.ReplaceAll(decoded, `\`, "/")
	if !strings.HasPrefix(decoded, "/") {
		return "", errors.New("image path must be absolute")
	}
	for _, character := range decoded {
		if character == 0 || character < 0x20 || character == 0x7f {
			return "", errors.New("image path contains control characters")
		}
	}
	canonical := pathpkg.Clean(decoded)
	if !strings.HasPrefix(canonical, "/wallpaper/") {
		return "", errors.New("image path is not allowed")
	}
	return canonical, nil
}

func hasPercentEncodedOctet(value string) bool {
	for index := 0; index+2 < len(value); index++ {
		if value[index] == '%' && isHexDigit(value[index+1]) && isHexDigit(value[index+2]) {
			return true
		}
	}
	return false
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func newUHDpaperTransport() http.RoundTripper {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		DialContext:           newUHDpaperDialContext(dialer, net.DefaultResolver),
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   7 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func newUHDpaperDialContext(dialer *net.Dialer, resolver *net.Resolver) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("split upstream address: %w", err)
		}
		addresses, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve upstream host: %w", err)
		}
		if len(addresses) == 0 {
			return nil, errors.New("upstream host resolved without addresses")
		}
		for _, candidate := range addresses {
			if !isPublicUHDpaperAddress(candidate) {
				return nil, errors.New("upstream host resolved to a non-public address")
			}
		}

		var dialErrors []error
		for _, candidate := range addresses {
			candidate = candidate.Unmap()
			if network == "tcp4" && !candidate.Is4() {
				continue
			}
			if network == "tcp6" && !candidate.Is6() {
				continue
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			dialErrors = append(dialErrors, dialErr)
		}
		if len(dialErrors) == 0 {
			return nil, errors.New("upstream host has no address compatible with the requested network")
		}
		return nil, errors.Join(dialErrors...)
	}
}

func isPublicUHDpaperAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsUnspecified() || address.IsLoopback() ||
		address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return false
	}
	for _, blocked := range uhdpaperBlockedNetworkPrefixes {
		if blocked.Contains(address) {
			return false
		}
	}
	return true
}

var uhdpaperBlockedNetworkPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

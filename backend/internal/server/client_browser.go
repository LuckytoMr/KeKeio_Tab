package server

import "strings"

type clientBrowser struct {
	Family  string
	Version string
}

type browserSignature struct {
	marker string
	family string
}

var clientBrowserSignatures = []browserSignature{
	{marker: "EdgiOS/", family: "edge"},
	{marker: "EdgA/", family: "edge"},
	{marker: "Edg/", family: "edge"},
	{marker: "OPR/", family: "opera"},
	{marker: "Vivaldi/", family: "vivaldi"},
	{marker: "YaBrowser/", family: "yandex"},
	{marker: "SamsungBrowser/", family: "samsung-internet"},
	{marker: "FxiOS/", family: "firefox"},
	{marker: "Firefox/", family: "firefox"},
	{marker: "CriOS/", family: "chrome"},
	{marker: "Chrome/", family: "chrome"},
	{marker: "Chromium/", family: "chromium"},
}

// identifyClientBrowser 只保留管理端展示所需的规范化浏览器类型与版本，
// 不存储完整 User-Agent，也不能把识别结果当作安全身份。
func identifyClientBrowser(userAgent string) clientBrowser {
	if len(userAgent) > 4096 {
		userAgent = userAgent[:4096]
	}
	for _, signature := range clientBrowserSignatures {
		if strings.Contains(userAgent, signature.marker) {
			return clientBrowser{Family: signature.family, Version: versionAfterMarker(userAgent, signature.marker)}
		}
	}
	if strings.Contains(userAgent, "Safari/") {
		if version := versionAfterMarker(userAgent, "Version/"); version != "" {
			return clientBrowser{Family: "safari", Version: version}
		}
	}
	return clientBrowser{Family: "unknown"}
}

func versionAfterMarker(userAgent, marker string) string {
	index := strings.Index(userAgent, marker)
	if index < 0 {
		return ""
	}
	start := index + len(marker)
	end := start
	for end < len(userAgent) && end-start < 32 {
		char := userAgent[end]
		if (char < '0' || char > '9') && char != '.' {
			break
		}
		end++
	}
	version := strings.Trim(userAgent[start:end], ".")
	if version == "" || version[0] < '0' || version[0] > '9' {
		return ""
	}
	return version
}

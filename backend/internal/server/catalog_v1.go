package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type catalogWallpaperV1 struct {
	ID            string                   `json:"id"`
	Provider      string                   `json:"provider"`
	SourcePageURL string                   `json:"sourcePageUrl,omitempty"`
	Title         string                   `json:"title"`
	Category      string                   `json:"category"`
	Tags          []string                 `json:"tags"`
	PreviewURL    string                   `json:"previewUrl,omitempty"`
	Variants      []WallpaperVariantRecord `json:"variants"`
}

type catalogStyleV1 struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	Version             string          `json:"version"`
	Description         string          `json:"description,omitempty"`
	PreviewURL          string          `json:"previewUrl,omitempty"`
	CSS                 string          `json:"css"`
	Config              json.RawMessage `json:"config,omitempty"`
	SHA256              string          `json:"sha256"`
	StyleSchemaVersion  int             `json:"styleSchemaVersion"`
	MinExtensionVersion string          `json:"minExtensionVersion"`
	Status              string          `json:"status"`
}

func (a *App) requirePluginAccessV1(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "需要有效 access token")
			return
		}
		if _, err := a.store.VerifiedUserByAccessToken(r.Context(), token); err != nil {
			if _, readErr := a.store.UserByAccessToken(r.Context(), token); readErr == nil {
				writeAPIError(w, http.StatusForbidden, "EMAIL_VERIFICATION_REQUIRED", "完成邮箱验证后才能访问内容目录")
				return
			}
			writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "access token 无效或已过期")
			return
		}
		next(w, r)
	}
}

func (a *App) handleBootstrapV1(w http.ResponseWriter, r *http.Request) {
	channel, validChannel := releaseChannelFromRequest(r)
	if !validChannel {
		writeAPIError(w, http.StatusBadRequest, "INVALID_RELEASE_CHANNEL", "版本通道必须是 stable 或 beta")
		return
	}
	summary, err := a.store.AdminSummary(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "BOOTSTRAP_UNAVAILABLE", "服务能力暂不可用")
		return
	}
	release, err := a.store.LatestPublishedRelease(r.Context(), channel)
	if err != nil && !errors.Is(err, ErrNotFound) {
		writeAPIError(w, http.StatusServiceUnavailable, "BOOTSTRAP_UNAVAILABLE", "版本信息暂不可用")
		return
	}
	var latestRelease any
	if err == nil {
		latestRelease = map[string]any{
			"id": release.ID, "version": release.Version, "channel": release.Channel,
			"notes": release.Notes, "downloadUrl": release.DownloadURL,
			"minimumVersion": release.MinimumVersion, "schemaVersion": release.SchemaVersion,
			"status": release.Status, "publishedAt": release.PublishedAt,
		}
	}
	writeAPIData(w, http.StatusOK, map[string]any{
		"service":       "full-pro-backend",
		"capabilities":  map[string]bool{"syncV2": true, "catalogV1": true, "rotatingRefreshTokens": true},
		"catalog":       map[string]int{"officialWallpapers": summary.OfficialWallpapers, "webWallpapers": summary.WebWallpapers, "styles": summary.Styles},
		"latestRelease": latestRelease,
	})
}

func releaseChannelFromRequest(r *http.Request) (string, bool) {
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	if channel == "" {
		channel = "stable"
	}
	return channel, oneOf(channel, "stable", "beta")
}

func (a *App) handleCatalogWebWallpapersV1(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	result, err := a.store.ListClientWebWallpapers(r.Context(), WallpaperListFilter{
		Category: r.URL.Query().Get("category"), Page: page, PageSize: pageSize,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "Web 壁纸目录暂不可用")
		return
	}
	writeAPIData(w, http.StatusOK, result)
}

func (a *App) handleCatalogOfficialWallpapersV1(w http.ResponseWriter, r *http.Request) {
	records, err := a.store.ListAdminOfficialWallpapers(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "官方壁纸目录暂不可用")
		return
	}
	items := make([]catalogWallpaperV1, 0, len(records))
	for _, record := range records {
		if !record.Enabled {
			continue
		}
		items = append(items, catalogWallpaperV1{
			ID: record.ID, Provider: "official", Title: record.Title, Category: record.Category,
			Tags: record.Tags, PreviewURL: record.PreviewURL, Variants: record.Variants,
		})
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleCatalogStylesV1(w http.ResponseWriter, r *http.Request) {
	records, err := a.store.ListPublicStylePackages(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "CATALOG_UNAVAILABLE", "风格目录暂不可用")
		return
	}
	items := make([]catalogStyleV1, 0, len(records))
	for _, record := range records {
		digest := sha256.Sum256([]byte(record.CSS))
		items = append(items, catalogStyleV1{
			ID: record.ID, Name: record.Name, Version: record.Version, Description: record.Description,
			PreviewURL: record.PreviewURL, CSS: record.CSS, Config: record.Config,
			SHA256: hex.EncodeToString(digest[:]), StyleSchemaVersion: 1,
			MinExtensionVersion: "0.1.0", Status: "published",
		})
	}
	writeAPIData(w, http.StatusOK, map[string]any{"items": items})
}

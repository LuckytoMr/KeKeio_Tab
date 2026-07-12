package server

import "encoding/json"

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type User struct {
	ID              string `json:"id"`
	Email           string `json:"email"`
	Role            Role   `json:"role"`
	CreatedAt       string `json:"createdAt"`
	LastLoginAt     string `json:"lastLoginAt,omitempty"`
	Status          string `json:"status,omitempty"`
	EmailVerifiedAt string `json:"emailVerifiedAt,omitempty"`
}

type ProfileRecord struct {
	UserID      string          `json:"userId"`
	ProfileJSON json.RawMessage `json:"profile"`
	Version     int             `json:"version"`
	UpdatedAt   string          `json:"updatedAt"`
}

type ProfileVersion struct {
	ID          string          `json:"id"`
	UserID      string          `json:"userId"`
	Version     int             `json:"version"`
	ProfileJSON json.RawMessage `json:"profile,omitempty"`
	CreatedAt   string          `json:"createdAt"`
}

type IdempotencyRecord struct {
	ID             string `json:"id"`
	UserID         string `json:"userId"`
	IdempotencyKey string `json:"idempotencyKey"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	RequestHash    string `json:"requestHash"`
	Status         int    `json:"status"`
	ResponseJSON   string `json:"responseJson"`
	CreatedAt      string `json:"createdAt"`
}

type WallpaperVariantRecord struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

type PublicWallpaperVariant struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type WebWallpaperRecord struct {
	ID            string                   `json:"id"`
	Provider      string                   `json:"provider"`
	SourcePageURL string                   `json:"sourcePageUrl,omitempty"`
	Title         string                   `json:"title"`
	Category      string                   `json:"category"`
	Tags          []string                 `json:"tags"`
	PreviewURL    string                   `json:"previewUrl,omitempty"`
	Variants      []WallpaperVariantRecord `json:"variants"`
	Enabled       bool                     `json:"enabled"`
	CachedAt      string                   `json:"cachedAt,omitempty"`
	UpdatedAt     string                   `json:"updatedAt,omitempty"`
}

type PublicWebWallpaper struct {
	ID       string                   `json:"id"`
	Provider string                   `json:"provider"`
	Title    string                   `json:"title"`
	Category string                   `json:"category"`
	Tags     []string                 `json:"tags"`
	Variants []PublicWallpaperVariant `json:"variants"`
}

type ClientWebWallpaper struct {
	ID            string                   `json:"id"`
	Provider      string                   `json:"provider"`
	SourcePageURL string                   `json:"sourcePageUrl,omitempty"`
	Title         string                   `json:"title"`
	Category      string                   `json:"category"`
	Tags          []string                 `json:"tags"`
	PreviewURL    string                   `json:"previewUrl,omitempty"`
	Variants      []WallpaperVariantRecord `json:"variants"`
}

type OfficialWallpaperRecord struct {
	ID         string                   `json:"id"`
	Title      string                   `json:"title"`
	Category   string                   `json:"category"`
	Tags       []string                 `json:"tags"`
	PreviewURL string                   `json:"previewUrl,omitempty"`
	Variants   []WallpaperVariantRecord `json:"variants"`
	Enabled    bool                     `json:"enabled"`
	SortIndex  int                      `json:"sortIndex"`
	CreatedAt  string                   `json:"createdAt,omitempty"`
	UpdatedAt  string                   `json:"updatedAt,omitempty"`
}

type PublicOfficialWallpaper struct {
	ID         string                   `json:"id"`
	Title      string                   `json:"title"`
	Category   string                   `json:"category"`
	Tags       []string                 `json:"tags"`
	PreviewURL string                   `json:"previewUrl,omitempty"`
	Variants   []PublicWallpaperVariant `json:"variants"`
}

type WallpaperListFilter struct {
	Category string
	Page     int
	PageSize int
}

type PublicWebWallpaperPage struct {
	Items    []PublicWebWallpaper `json:"items"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"pageSize"`
	Total    int                  `json:"total"`
}

type ClientWebWallpaperPage struct {
	Items    []ClientWebWallpaper `json:"items"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"pageSize"`
	Total    int                  `json:"total"`
}

type ReleaseRecord struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Channel        string `json:"channel"`
	Notes          string `json:"notes"`
	DownloadURL    string `json:"downloadUrl"`
	MinimumVersion string `json:"minimumVersion"`
	SchemaVersion  int    `json:"schemaVersion"`
	Status         string `json:"status"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	PublishedAt    string `json:"publishedAt,omitempty"`
	DisabledAt     string `json:"disabledAt,omitempty"`
}

type StylePackageRecord struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	PreviewURL  string          `json:"previewUrl,omitempty"`
	CSS         string          `json:"css"`
	ConfigJSON  json.RawMessage `json:"config,omitempty"`
	Enabled     bool            `json:"enabled"`
	SortIndex   int             `json:"sortIndex"`
	CreatedAt   string          `json:"createdAt,omitempty"`
	UpdatedAt   string          `json:"updatedAt,omitempty"`
}

type PublicStylePackage struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	PreviewURL  string          `json:"previewUrl,omitempty"`
	CSS         string          `json:"css"`
	Config      json.RawMessage `json:"config,omitempty"`
}

type APILogRecord struct {
	ID             string `json:"id"`
	CreatedAt      string `json:"createdAt"`
	UserID         string `json:"userId,omitempty"`
	UserEmail      string `json:"userEmail,omitempty"`
	Role           string `json:"role,omitempty"`
	IP             string `json:"ip"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	RouteGroup     string `json:"routeGroup"`
	Status         int    `json:"status"`
	DurationMS     int64  `json:"durationMs"`
	RequestBytes   int64  `json:"requestBytes"`
	ResponseBytes  int64  `json:"responseBytes"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	UserAgent      string `json:"userAgent,omitempty"`
	Error          string `json:"error,omitempty"`
}

type APILogFilter struct {
	UserEmail  string
	RouteGroup string
	Status     int
	Limit      int
}

type AdminSummary struct {
	Users              int `json:"users"`
	Profiles           int `json:"profiles"`
	ProfileVersions    int `json:"profileVersions"`
	OfficialWallpapers int `json:"officialWallpapers"`
	WebWallpapers      int `json:"webWallpapers"`
	Releases           int `json:"releases"`
	Styles             int `json:"styles"`
	APILogs            int `json:"apiLogs"`
}

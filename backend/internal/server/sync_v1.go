package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrIdempotencyMismatch = errors.New("idempotency mismatch")

type SyncProfileRecord struct {
	ProfileJSON      json.RawMessage `json:"profile"`
	Version          int             `json:"version"`
	SchemaVersion    int             `json:"schemaVersion"`
	UpdatedAt        string          `json:"updatedAt"`
	IdempotentReplay bool            `json:"idempotentReplay"`
	ProfileHash      string          `json:"profileHash"`
	MutationID       string          `json:"mutationId,omitempty"`
}

type SyncConflictDetails struct {
	ConflictID     string          `json:"conflictId"`
	BaseVersion    int             `json:"baseVersion"`
	CurrentVersion int             `json:"currentVersion"`
	CurrentProfile json.RawMessage `json:"currentProfile"`
	CurrentHash    string          `json:"currentHash"`
}

type SyncMutationRequest struct {
	BaseVersion        int             `json:"baseVersion"`
	MutationID         string          `json:"mutationId"`
	DeviceID           string          `json:"deviceId"`
	SchemaVersion      int             `json:"schemaVersion"`
	Profile            json.RawMessage `json:"profile"`
	ResolvesConflictID string          `json:"resolvesConflictId,omitempty"`
}

func ValidateSharedProfileV2(raw json.RawMessage, maxBytes int) error {
	if maxBytes <= 0 {
		maxBytes = 512 << 10
	}
	if len(raw) == 0 || len(raw) > maxBytes {
		return fmt.Errorf("profile must contain between 1 and %d bytes", maxBytes)
	}
	if err := requireSharedProfileFields(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var profile sharedProfileV2
	if err := decoder.Decode(&profile); err != nil {
		return fmt.Errorf("profile schema: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("profile contains trailing JSON")
	}
	return profile.validate()
}

func requireSharedProfileFields(raw json.RawMessage) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil || root == nil {
		return fmt.Errorf("profile must be a JSON object")
	}
	if err := requireJSONKeys(root, "profile", "schemaVersion", "profileId", "updatedAt", "groups", "shortcuts", "search", "wallpaper", "theme"); err != nil {
		return err
	}
	for key, required := range map[string][]string{
		"search":    {"mode", "disposition", "selectedEngineId", "engines"},
		"wallpaper": {"selected", "selectedIds", "rotationMode", "rotationSource", "rotationIntervalSeconds", "overlayOpacity", "blur"},
		"theme":     {"styleId", "density", "sidebarSide", "showBrand", "columns", "rows", "iconSize", "iconShape"},
	} {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(root[key], &object); err != nil || object == nil {
			return fmt.Errorf("%s must be an object", key)
		}
		if err := requireJSONKeys(object, key, required...); err != nil {
			return err
		}
	}
	var selected map[string]json.RawMessage
	var wallpaper map[string]json.RawMessage
	_ = json.Unmarshal(root["wallpaper"], &wallpaper)
	if err := json.Unmarshal(wallpaper["selected"], &selected); err != nil || selected == nil {
		return fmt.Errorf("wallpaper.selected must be an object")
	}
	if err := requireJSONKeys(selected, "wallpaper.selected", "kind", "id"); err != nil {
		return err
	}
	for key, required := range map[string][]string{
		"groups":    {"id", "title", "sortIndex", "createdAt", "updatedAt"},
		"shortcuts": {"id", "groupId", "title", "url", "icon", "sortIndex", "createdAt", "updatedAt"},
	} {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(root[key], &items); err != nil || items == nil {
			return fmt.Errorf("%s must be an array", key)
		}
		for index, object := range items {
			if err := requireJSONKeys(object, fmt.Sprintf("%s[%d]", key, index), required...); err != nil {
				return err
			}
			if key == "shortcuts" {
				var icon map[string]json.RawMessage
				if err := json.Unmarshal(object["icon"], &icon); err != nil || icon == nil {
					return fmt.Errorf("shortcuts[%d].icon must be an object", index)
				}
				if err := requireJSONKeys(icon, fmt.Sprintf("shortcuts[%d].icon", index), "kind"); err != nil {
					return err
				}
			}
		}
	}
	var search map[string]json.RawMessage
	_ = json.Unmarshal(root["search"], &search)
	var engines []map[string]json.RawMessage
	if err := json.Unmarshal(search["engines"], &engines); err != nil || engines == nil {
		return fmt.Errorf("search.engines must be an array")
	}
	for index, engine := range engines {
		if err := requireJSONKeys(engine, fmt.Sprintf("search.engines[%d]", index), "id", "title", "template"); err != nil {
			return err
		}
	}
	return nil
}

func requireJSONKeys(object map[string]json.RawMessage, path string, keys ...string) error {
	for _, key := range keys {
		if _, found := object[key]; !found {
			return fmt.Errorf("%s.%s is required", path, key)
		}
	}
	return nil
}

type sharedProfileV2 struct {
	SchemaVersion int              `json:"schemaVersion"`
	ProfileID     string           `json:"profileId"`
	UpdatedAt     string           `json:"updatedAt"`
	Groups        []sharedGroup    `json:"groups"`
	Shortcuts     []sharedShortcut `json:"shortcuts"`
	Search        sharedSearch     `json:"search"`
	Wallpaper     sharedWallpaper  `json:"wallpaper"`
	Theme         sharedTheme      `json:"theme"`
}

type sharedGroup struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	SortIndex int    `json:"sortIndex"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	DeletedAt string `json:"deletedAt,omitempty"`
}

type sharedShortcut struct {
	ID        string     `json:"id"`
	GroupID   string     `json:"groupId"`
	Title     string     `json:"title"`
	URL       string     `json:"url"`
	Icon      sharedIcon `json:"icon"`
	SortIndex int        `json:"sortIndex"`
	CreatedAt string     `json:"createdAt"`
	UpdatedAt string     `json:"updatedAt"`
	DeletedAt string     `json:"deletedAt,omitempty"`
}

type sharedIcon struct {
	Kind         string `json:"kind"`
	Text         string `json:"text,omitempty"`
	BG           string `json:"bg,omitempty"`
	FG           string `json:"fg,omitempty"`
	URL          string `json:"url,omitempty"`
	FallbackText string `json:"fallbackText,omitempty"`
	PresetID     string `json:"presetId,omitempty"`
}

type sharedSearch struct {
	Mode             string               `json:"mode"`
	Disposition      string               `json:"disposition"`
	SelectedEngineID string               `json:"selectedEngineId"`
	Engines          []sharedSearchEngine `json:"engines"`
}

type sharedSearchEngine struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Template string `json:"template"`
}

type sharedWallpaper struct {
	Selected                sharedWallpaperSelection `json:"selected"`
	SelectedIDs             []string                 `json:"selectedIds"`
	RotationMode            string                   `json:"rotationMode"`
	RotationSource          string                   `json:"rotationSource"`
	RotationIntervalSeconds int                      `json:"rotationIntervalSeconds"`
	OverlayOpacity          float64                  `json:"overlayOpacity"`
	Blur                    float64                  `json:"blur"`
}

type sharedWallpaperSelection struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	VariantID string `json:"variantId,omitempty"`
}

type sharedTheme struct {
	StyleID     string `json:"styleId"`
	Density     string `json:"density"`
	SidebarSide string `json:"sidebarSide"`
	ShowBrand   bool   `json:"showBrand"`
	Columns     int    `json:"columns"`
	Rows        int    `json:"rows"`
	IconSize    string `json:"iconSize"`
	IconShape   string `json:"iconShape"`
}

func (profile sharedProfileV2) validate() error {
	if profile.SchemaVersion != 2 || !validIdentifier(profile.ProfileID) || !validTimestamp(profile.UpdatedAt) {
		return fmt.Errorf("schemaVersion, profileId, or updatedAt is invalid")
	}
	if len(profile.Groups) > 256 || len(profile.Shortcuts) > 4096 || len(profile.Search.Engines) > 128 || len(profile.Wallpaper.SelectedIDs) > 1024 {
		return fmt.Errorf("profile collection exceeds its limit")
	}
	groupIDs := map[string]struct{}{}
	for _, group := range profile.Groups {
		if !validIdentifier(group.ID) || !validText(group.Title, 1, 160) || group.SortIndex < 0 || !validTimestamp(group.CreatedAt) || !validTimestamp(group.UpdatedAt) || (group.DeletedAt != "" && !validTimestamp(group.DeletedAt)) {
			return fmt.Errorf("group is invalid")
		}
		if _, duplicate := groupIDs[group.ID]; duplicate {
			return fmt.Errorf("duplicate group id")
		}
		groupIDs[group.ID] = struct{}{}
	}
	shortcutIDs := map[string]struct{}{}
	for _, shortcut := range profile.Shortcuts {
		if !validIdentifier(shortcut.ID) || !validIdentifier(shortcut.GroupID) || !validText(shortcut.Title, 1, 240) || shortcut.SortIndex < 0 || !validTimestamp(shortcut.CreatedAt) || !validTimestamp(shortcut.UpdatedAt) || (shortcut.DeletedAt != "" && !validTimestamp(shortcut.DeletedAt)) || !validHTTPURL(shortcut.URL) {
			return fmt.Errorf("shortcut is invalid")
		}
		if _, found := groupIDs[shortcut.GroupID]; !found {
			return fmt.Errorf("shortcut references an unknown group")
		}
		if _, duplicate := shortcutIDs[shortcut.ID]; duplicate {
			return fmt.Errorf("duplicate shortcut id")
		}
		shortcutIDs[shortcut.ID] = struct{}{}
		if err := shortcut.Icon.validate(); err != nil {
			return err
		}
	}
	if !oneOf(profile.Search.Mode, "browser-default", "custom") || !oneOf(profile.Search.Disposition, "CURRENT_TAB", "NEW_TAB") || !validIdentifier(profile.Search.SelectedEngineID) {
		return fmt.Errorf("search settings are invalid")
	}
	engineIDs := map[string]struct{}{}
	for _, engine := range profile.Search.Engines {
		if !validIdentifier(engine.ID) || !validText(engine.Title, 1, 120) || !validText(engine.Template, 1, 2048) {
			return fmt.Errorf("search engine is invalid")
		}
		if _, duplicate := engineIDs[engine.ID]; duplicate {
			return fmt.Errorf("duplicate search engine id")
		}
		engineIDs[engine.ID] = struct{}{}
	}
	if _, found := engineIDs[profile.Search.SelectedEngineID]; profile.Search.Mode == "custom" && !found {
		return fmt.Errorf("selected search engine is missing")
	}
	selected := profile.Wallpaper.Selected
	if !oneOf(selected.Kind, "builtin", "remote") || !validIdentifier(selected.ID) || (selected.Kind == "remote" && !validIdentifier(selected.VariantID)) || (selected.Kind == "builtin" && selected.VariantID != "") {
		return fmt.Errorf("wallpaper selection is invalid")
	}
	for _, id := range profile.Wallpaper.SelectedIDs {
		if !validText(id, 1, 512) || strings.HasPrefix(id, "local:") {
			return fmt.Errorf("wallpaper selectedIds contains a local or invalid id")
		}
	}
	if !oneOf(profile.Wallpaper.RotationMode, "manual", "random") || !oneOf(profile.Wallpaper.RotationSource, "selected", "web") || profile.Wallpaper.RotationIntervalSeconds < 1 || profile.Wallpaper.RotationIntervalSeconds > 86400 || profile.Wallpaper.OverlayOpacity < 0 || profile.Wallpaper.OverlayOpacity > 1 || profile.Wallpaper.Blur < 0 || profile.Wallpaper.Blur > 100 {
		return fmt.Errorf("wallpaper settings are invalid")
	}
	if !validIdentifier(profile.Theme.StyleID) || !oneOf(profile.Theme.Density, "comfortable", "compact") || !oneOf(profile.Theme.SidebarSide, "left", "right") || !oneOf(profile.Theme.IconSize, "tiny", "mini", "small", "medium", "large", "xlarge") || !oneOf(profile.Theme.IconShape, "circle", "rounded", "squircle", "soft") || profile.Theme.Columns < 4 || profile.Theme.Columns > 8 || profile.Theme.Rows < 1 || profile.Theme.Rows > 5 {
		return fmt.Errorf("theme settings are invalid")
	}
	return nil
}

func (icon sharedIcon) validate() error {
	switch icon.Kind {
	case "text":
		if !validText(icon.Text, 1, 32) || len(icon.BG) > 64 || len(icon.FG) > 64 || icon.URL != "" || icon.FallbackText != "" || icon.PresetID != "" {
			return fmt.Errorf("text icon is invalid")
		}
	case "favicon", "url":
		if !validHTTPSURL(icon.URL) || !validText(icon.FallbackText, 1, 32) || icon.Text != "" || icon.BG != "" || icon.FG != "" || icon.PresetID != "" {
			return fmt.Errorf("URL icon is invalid")
		}
	case "preset":
		if !validIdentifier(icon.PresetID) || icon.Text != "" || icon.BG != "" || icon.FG != "" || icon.URL != "" || icon.FallbackText != "" {
			return fmt.Errorf("preset icon is invalid")
		}
	default:
		return fmt.Errorf("icon kind is invalid")
	}
	return nil
}

func validIdentifier(value string) bool { return validText(value, 1, 256) }

func validText(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && length >= minimum && length <= maximum
}

func validTimestamp(value string) bool {
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}

func validHTTPURL(value string) bool {
	if len(value) == 0 || len(value) > 2048 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}

func validHTTPSURL(value string) bool {
	if len(value) == 0 || len(value) > 2048 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func findForbiddenProfileKey(value any, forbidden map[string]struct{}) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, found := forbidden[key]; found {
				return key
			}
			if nested := findForbiddenProfileKey(child, forbidden); nested != "" {
				return nested
			}
		}
	case []any:
		for _, child := range typed {
			if nested := findForbiddenProfileKey(child, forbidden); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func collectEntityIDs(value any) map[string]struct{} {
	result := map[string]struct{}{}
	items, _ := value.([]any)
	for _, item := range items {
		object, _ := item.(map[string]any)
		if id, _ := object["id"].(string); id != "" {
			result[id] = struct{}{}
		}
	}
	return result
}

func validateStableEntities(value any, kind string, groupIDs map[string]struct{}) error {
	if value == nil {
		return nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > 5000 {
		return fmt.Errorf("%ss must be an array with at most 5000 entries", kind)
	}
	seen := map[string]struct{}{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", kind)
		}
		id, _ := object["id"].(string)
		if strings.TrimSpace(id) == "" || len(id) > 128 {
			return fmt.Errorf("%s id is required", kind)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate %s id", kind)
		}
		seen[id] = struct{}{}
		if kind == "shortcut" {
			if rawURL, _ := object["url"].(string); rawURL != "" {
				parsed, err := url.Parse(rawURL)
				if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
					return fmt.Errorf("shortcut URL must use HTTP or HTTPS without credentials")
				}
			}
			if groupID, _ := object["groupId"].(string); groupID != "" && len(groupIDs) > 0 {
				if _, found := groupIDs[groupID]; !found {
					return fmt.Errorf("shortcut references an unknown group")
				}
			}
		}
	}
	return nil
}

func (s *Store) ApplySyncMutation(ctx context.Context, userID string, request SyncMutationRequest, requestHash string) (int, string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	var storedHash, storedResponse string
	var storedStatus int
	err = tx.QueryRowContext(ctx,
		`SELECT request_hash, status, response_json FROM sync_mutations WHERE user_id = ? AND mutation_id = ?`, userID, request.MutationID,
	).Scan(&storedHash, &storedStatus, &storedResponse)
	if err == nil {
		if storedHash != requestHash {
			return 0, "", false, ErrIdempotencyMismatch
		}
		if err := tx.Commit(); err != nil {
			return 0, "", false, err
		}
		return storedStatus, storedResponse, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, err
	}

	currentVersion := 0
	currentProfile := json.RawMessage(`null`)
	currentHash := "server-empty"
	var currentRaw string
	err = tx.QueryRowContext(ctx, `SELECT profile_json, version, profile_hash FROM sync_profiles WHERE user_id = ?`, userID).Scan(&currentRaw, &currentVersion, &currentHash)
	if err == nil {
		currentProfile = json.RawMessage(currentRaw)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, err
	}
	estimatedGrowth := int64(len(request.Profile) + len(request.MutationID) + len(request.DeviceID) + len(requestHash) + 4096)
	if currentVersion == 0 {
		estimatedGrowth += int64(len(request.Profile))
	} else if len(request.Profile) > len(currentRaw) {
		estimatedGrowth += int64(len(request.Profile) - len(currentRaw))
	}
	if err := enforceSyncStorageQuota(ctx, tx, estimatedGrowth); err != nil {
		return 0, "", false, err
	}
	now := nowString()
	if request.BaseVersion != currentVersion {
		conflict := SyncConflictDetails{ConflictID: newID("conflict_"), BaseVersion: request.BaseVersion, CurrentVersion: currentVersion, CurrentProfile: currentProfile, CurrentHash: currentHash}
		response, _ := json.Marshal(conflict)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sync_conflicts (id,user_id,mutation_id,device_id,base_version,current_version,status,created_at) VALUES (?,?,?,?,?,?,'open',?)`,
			conflict.ConflictID, userID, request.MutationID, request.DeviceID, request.BaseVersion, currentVersion, now,
		); err != nil {
			return 0, "", false, err
		}
		if err := insertSyncMutation(ctx, tx, userID, request, requestHash, currentVersion, http.StatusConflict, string(response), now); err != nil {
			return 0, "", false, err
		}
		if err := upsertSyncDeviceAndAttempt(ctx, tx, userID, request, currentVersion, http.StatusConflict, "PROFILE_CONFLICT", now); err != nil {
			return 0, "", false, err
		}
		if err := enforceSyncStorageQuota(ctx, tx, 0); err != nil {
			return 0, "", false, err
		}
		if err := tx.Commit(); err != nil {
			return 0, "", false, err
		}
		return http.StatusConflict, string(response), false, nil
	}

	if request.ResolvesConflictID != "" {
		var conflictStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM sync_conflicts WHERE id = ? AND user_id = ?`, request.ResolvesConflictID, userID).Scan(&conflictStatus); err != nil || conflictStatus != "open" {
			return 0, "", false, fmt.Errorf("invalid conflict resolution")
		}
	}
	nextVersion := currentVersion + 1
	profileHash, err := canonicalProfileHash(request.Profile)
	if err != nil {
		return 0, "", false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO legacy_profile_backups(user_id,profile_json,legacy_version,legacy_updated_at,archived_at)
		 SELECT user_id,profile_json,version,updated_at,? FROM profiles WHERE user_id=?`, now, userID,
	); err != nil {
		return 0, "", false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM profiles WHERE user_id=?`, userID); err != nil {
		return 0, "", false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sync_profiles (user_id,profile_json,version,schema_version,profile_hash,updated_at) VALUES (?,?,?,?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET profile_json=excluded.profile_json,version=excluded.version,schema_version=excluded.schema_version,profile_hash=excluded.profile_hash,updated_at=excluded.updated_at`,
		userID, string(request.Profile), nextVersion, request.SchemaVersion, profileHash, now,
	); err != nil {
		return 0, "", false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sync_profile_versions (id,user_id,version,schema_version,profile_json,profile_hash,mutation_id,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		newID("pver_"), userID, nextVersion, request.SchemaVersion, string(request.Profile), profileHash, request.MutationID, now,
	); err != nil {
		return 0, "", false, err
	}
	record := SyncProfileRecord{ProfileJSON: request.Profile, Version: nextVersion, SchemaVersion: request.SchemaVersion, UpdatedAt: now, ProfileHash: profileHash, MutationID: request.MutationID}
	response, _ := json.Marshal(record)
	if err := insertSyncMutation(ctx, tx, userID, request, requestHash, nextVersion, http.StatusOK, string(response), now); err != nil {
		return 0, "", false, err
	}
	if err := upsertSyncDeviceAndAttempt(ctx, tx, userID, request, nextVersion, http.StatusOK, "", now); err != nil {
		return 0, "", false, err
	}
	if request.ResolvesConflictID != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sync_conflicts SET status='resolved', resolved_by_mutation_id=?, resolved_at=? WHERE id=? AND user_id=? AND status='open'`,
			request.MutationID, now, request.ResolvesConflictID, userID,
		); err != nil {
			return 0, "", false, err
		}
	}
	if err := enforceSyncStorageQuota(ctx, tx, 0); err != nil {
		return 0, "", false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", false, err
	}
	return http.StatusOK, string(response), false, nil
}

func insertSyncMutation(ctx context.Context, tx *sql.Tx, userID string, request SyncMutationRequest, requestHash string, resultVersion, status int, response, now string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO sync_mutations (id,user_id,mutation_id,route,request_hash,base_version,result_version,status,response_json,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		newID("mutation_"), userID, request.MutationID, "/api/v1/sync/profile", requestHash, request.BaseVersion, resultVersion, status, response, now,
	)
	return err
}

func upsertSyncDeviceAndAttempt(ctx context.Context, tx *sql.Tx, userID string, request SyncMutationRequest, resultVersion, status int, errorCode, now string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO devices (user_id,device_id,first_seen_at,last_seen_at,last_version,revoked_at) VALUES (?,?,?,?,?,'')
		 ON CONFLICT(user_id,device_id) DO UPDATE SET last_seen_at=excluded.last_seen_at,last_version=excluded.last_version`,
		userID, request.DeviceID, now, now, resultVersion,
	); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO sync_attempts (id,user_id,device_id,mutation_id,base_version,result_version,status,error_code,created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		newID("attempt_"), userID, request.DeviceID, request.MutationID, request.BaseVersion, resultVersion, status, errorCode, now,
	)
	return err
}

func (s *Store) GetSyncProfile(ctx context.Context, userID string) (SyncProfileRecord, error) {
	var record SyncProfileRecord
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT profile_json,version,schema_version,profile_hash,updated_at FROM sync_profiles WHERE user_id=?`, userID,
	).Scan(&raw, &record.Version, &record.SchemaVersion, &record.ProfileHash, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncProfileRecord{}, ErrNotFound
	}
	if err != nil {
		return SyncProfileRecord{}, err
	}
	record.ProfileJSON = json.RawMessage(raw)
	return record, nil
}

func (a *App) handlePutSyncProfileV1(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	user, err := a.store.VerifiedUserByAccessToken(r.Context(), token)
	if err != nil {
		if _, readErr := a.store.UserByAccessToken(r.Context(), token); readErr == nil {
			writeAPIError(w, http.StatusForbidden, "EMAIL_VERIFICATION_REQUIRED", "完成邮箱验证后才能写入同步配置")
			return
		}
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "登录已失效")
		return
	}
	raw, ok := a.readRequestBody(w, r)
	if !ok {
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request SyncMutationRequest
	if err := decoder.Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "同步请求格式无效")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "同步请求只能包含一个 JSON 值")
		return
	}
	if request.SchemaVersion != 2 {
		writeAPIError(w, http.StatusUnprocessableEntity, "SCHEMA_UNSUPPORTED", "仅支持 SharedProfile schemaVersion 2")
		return
	}
	if strings.TrimSpace(request.MutationID) == "" || len(request.MutationID) > 128 || strings.TrimSpace(request.DeviceID) == "" || len(request.DeviceID) > 128 || request.BaseVersion < 0 {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "baseVersion、mutationId、deviceId 和 schemaVersion 无效")
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key == "" || key != request.MutationID {
		writeAPIError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key 必须与 mutationId 一致")
		return
	}
	profileLimit, err := a.store.PersistedLimit(r.Context(), "profileBytes", 512<<10)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "LIMITS_UNAVAILABLE", "无法读取同步配额")
		return
	}
	if len(request.Profile) > profileLimit {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "PROFILE_QUOTA_EXCEEDED", "同步配置超过账号配额")
		return
	}
	if err := ValidateSharedProfileV2(request.Profile, profileLimit); err != nil {
		writeAPIError(w, http.StatusBadRequest, "PROFILE_INVALID", err.Error())
		return
	}
	status, response, replay, err := a.store.ApplySyncMutation(r.Context(), user.ID, request, requestBodyHash(raw))
	if errors.Is(err, ErrIdempotencyMismatch) {
		writeAPIError(w, http.StatusConflict, "IDEMPOTENCY_MISMATCH", "相同 mutationId 的请求体不一致")
		return
	}
	if errors.Is(err, ErrStorageQuotaExceeded) {
		writeAPIError(w, http.StatusInsufficientStorage, "STORAGE_QUOTA_EXCEEDED", "同步存储空间已达上限")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "SYNC_REJECTED", err.Error())
		return
	}
	if status == http.StatusConflict {
		var details SyncConflictDetails
		_ = json.Unmarshal([]byte(response), &details)
		writeAPIErrorDetails(w, http.StatusConflict, "PROFILE_CONFLICT", "云端配置已更新", details)
		return
	}
	var data map[string]any
	_ = json.Unmarshal([]byte(response), &data)
	data["idempotentReplay"] = replay
	writeAPIData(w, http.StatusOK, data)
}

func (a *App) handleGetSyncProfileV1(w http.ResponseWriter, r *http.Request) {
	user, err := a.store.UserByAccessToken(r.Context(), bearerToken(r))
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "登录已失效")
		return
	}
	record, err := a.store.GetSyncProfile(r.Context(), user.ID)
	if errors.Is(err, ErrNotFound) {
		writeAPIData(w, http.StatusOK, map[string]any{"profile": nil, "version": 0, "profileHash": nil, "schemaVersion": 2})
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "PROFILE_READ_FAILED", "无法读取同步配置")
		return
	}
	writeAPIData(w, http.StatusOK, record)
}

func canonicalProfileHash(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("profile contains trailing JSON")
	}
	var canonical bytes.Buffer
	if err := writeExtensionCanonicalJSON(&canonical, value); err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

func writeExtensionCanonicalJSON(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if typed {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case json.Number:
		output.WriteString(typed.String())
	case string:
		writeJSONStringLikeJavaScript(output, typed)
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeExtensionCanonicalJSON(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			writeJSONStringLikeJavaScript(output, key)
			output.WriteByte(':')
			if err := writeExtensionCanonicalJSON(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON value %T", value)
	}
	return nil
}

func writeJSONStringLikeJavaScript(output *bytes.Buffer, value string) {
	const hexadecimal = "0123456789abcdef"
	output.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"', '\\':
			output.WriteByte('\\')
			output.WriteRune(character)
		case '\b':
			output.WriteString(`\b`)
		case '\f':
			output.WriteString(`\f`)
		case '\n':
			output.WriteString(`\n`)
		case '\r':
			output.WriteString(`\r`)
		case '\t':
			output.WriteString(`\t`)
		default:
			if character < 0x20 {
				output.WriteString(`\u00`)
				output.WriteByte(hexadecimal[byte(character)>>4])
				output.WriteByte(hexadecimal[byte(character)&0x0f])
			} else {
				output.WriteRune(character)
			}
		}
	}
	output.WriteByte('"')
}

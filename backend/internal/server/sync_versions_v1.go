package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	defaultSyncVersionV1Limit = 20
	maxSyncVersionV1Limit     = 100
)

type syncProfileVersionV1 struct {
	ID            string `json:"id"`
	Version       int    `json:"version"`
	SchemaVersion int    `json:"schemaVersion"`
	ProfileHash   string `json:"profileHash"`
	MutationID    string `json:"mutationId"`
	CreatedAt     string `json:"createdAt"`
}

type syncProfileVersionsPageV1 struct {
	Items      []syncProfileVersionV1 `json:"items"`
	NextCursor string                 `json:"nextCursor"`
	Limit      int                    `json:"limit"`
}

type syncProfileVersionForRestoreV1 struct {
	SchemaVersion int
	ProfileJSON   json.RawMessage
}

type syncProfileRestoreRequestV1 struct {
	BaseVersion        *int   `json:"baseVersion"`
	MutationID         string `json:"mutationId"`
	DeviceID           string `json:"deviceId"`
	ResolvesConflictID string `json:"resolvesConflictId,omitempty"`
}

func (a *App) registerSyncVersionV1Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/sync/profile/versions", a.handleListSyncProfileVersionsV1)
	mux.HandleFunc("POST /api/v1/sync/profile/versions/{id}/restore", a.handleRestoreSyncProfileVersionV1)
}

func (a *App) handleListSyncProfileVersionsV1(w http.ResponseWriter, r *http.Request) {
	user, err := a.store.UserByAccessToken(r.Context(), bearerToken(r))
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "登录已失效")
		return
	}

	limit := defaultSyncVersionV1Limit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if parsed, parseErr := strconv.Atoi(rawLimit); parseErr == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > maxSyncVersionV1Limit {
		limit = maxSyncVersionV1Limit
	}

	beforeVersion := 0
	if cursor := strings.TrimSpace(r.URL.Query().Get("cursor")); cursor != "" {
		parsed, parseErr := strconv.Atoi(cursor)
		if parseErr != nil || parsed < 1 {
			writeAPIError(w, http.StatusBadRequest, "INVALID_CURSOR", "版本游标无效")
			return
		}
		beforeVersion = parsed
	}

	items, nextCursor, err := a.store.listSyncProfileVersionsV1(r.Context(), user.ID, beforeVersion, limit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "PROFILE_VERSIONS_READ_FAILED", "无法读取同步版本")
		return
	}
	writeAPIData(w, http.StatusOK, syncProfileVersionsPageV1{Items: items, NextCursor: nextCursor, Limit: limit})
}

func (a *App) handleRestoreSyncProfileVersionV1(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	user, err := a.store.VerifiedUserByAccessToken(r.Context(), token)
	if err != nil {
		if _, readErr := a.store.UserByAccessToken(r.Context(), token); readErr == nil {
			writeAPIError(w, http.StatusForbidden, "EMAIL_VERIFICATION_REQUIRED", "完成邮箱验证后才能恢复同步版本")
			return
		}
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "登录已失效")
		return
	}

	request, ok := a.decodeSyncProfileRestoreRequestV1(w, r)
	if !ok {
		return
	}
	versionID := strings.TrimSpace(r.PathValue("id"))
	if versionID == "" {
		writeAPIError(w, http.StatusNotFound, "PROFILE_VERSION_NOT_FOUND", "同步版本不存在")
		return
	}
	target, err := a.store.syncProfileVersionForRestoreV1(r.Context(), user.ID, versionID)
	if errors.Is(err, ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "PROFILE_VERSION_NOT_FOUND", "同步版本不存在")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "PROFILE_VERSION_READ_FAILED", "无法读取同步版本")
		return
	}

	mutation := SyncMutationRequest{
		BaseVersion:        *request.BaseVersion,
		MutationID:         request.MutationID,
		DeviceID:           request.DeviceID,
		SchemaVersion:      target.SchemaVersion,
		Profile:            target.ProfileJSON,
		ResolvesConflictID: request.ResolvesConflictID,
	}
	rawMutation, err := json.Marshal(mutation)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "PROFILE_RESTORE_FAILED", "无法构造恢复请求")
		return
	}

	putRequest := r.Clone(r.Context())
	putRequest.Method = http.MethodPut
	putRequest.URL.Path = "/api/v1/sync/profile"
	putRequest.URL.RawPath = ""
	putRequest.RequestURI = "/api/v1/sync/profile"
	putRequest.Header = r.Header.Clone()
	putRequest.Header.Set("Content-Type", "application/json")
	putRequest.Header.Set("Idempotency-Key", request.MutationID)
	putRequest.Body = io.NopCloser(bytes.NewReader(rawMutation))
	putRequest.ContentLength = int64(len(rawMutation))
	a.handlePutSyncProfileV1(w, putRequest)
}

func (a *App) decodeSyncProfileRestoreRequestV1(w http.ResponseWriter, r *http.Request) (syncProfileRestoreRequestV1, bool) {
	defer r.Body.Close()
	maxBytes := a.config.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "恢复请求格式无效")
		return syncProfileRestoreRequestV1{}, false
	}
	if int64(len(raw)) > maxBytes {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "恢复请求过大")
		return syncProfileRestoreRequestV1{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request syncProfileRestoreRequestV1
	if err := decoder.Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "恢复请求格式无效")
		return syncProfileRestoreRequestV1{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "恢复请求只能包含一个 JSON 值")
		return syncProfileRestoreRequestV1{}, false
	}
	request.MutationID = strings.TrimSpace(request.MutationID)
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	request.ResolvesConflictID = strings.TrimSpace(request.ResolvesConflictID)
	if request.BaseVersion == nil || *request.BaseVersion < 0 || request.MutationID == "" || len(request.MutationID) > 128 || request.DeviceID == "" || len(request.DeviceID) > 128 || len(request.ResolvesConflictID) > 128 {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "baseVersion、mutationId 和 deviceId 无效")
		return syncProfileRestoreRequestV1{}, false
	}
	return request, true
}

func (s *Store) listSyncProfileVersionsV1(ctx context.Context, userID string, beforeVersion, limit int) ([]syncProfileVersionV1, string, error) {
	query := `SELECT id,version,schema_version,profile_hash,mutation_id,created_at
		FROM sync_profile_versions WHERE user_id=?`
	args := []any{userID}
	if beforeVersion > 0 {
		query += ` AND version < ?`
		args = append(args, beforeVersion)
	}
	query += ` ORDER BY version DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]syncProfileVersionV1, 0, limit)
	for rows.Next() {
		var item syncProfileVersionV1
		if err := rows.Scan(&item.ID, &item.Version, &item.SchemaVersion, &item.ProfileHash, &item.MutationID, &item.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor = strconv.Itoa(items[len(items)-1].Version)
	}
	return items, nextCursor, nil
}

func (s *Store) syncProfileVersionForRestoreV1(ctx context.Context, userID, versionID string) (syncProfileVersionForRestoreV1, error) {
	var target syncProfileVersionForRestoreV1
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT schema_version,profile_json FROM sync_profile_versions WHERE id=? AND user_id=?`,
		versionID, userID,
	).Scan(&target.SchemaVersion, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return syncProfileVersionForRestoreV1{}, ErrNotFound
	}
	if err != nil {
		return syncProfileVersionForRestoreV1{}, err
	}
	target.ProfileJSON = json.RawMessage(raw)
	return target, nil
}

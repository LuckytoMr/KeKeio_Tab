package server

import (
	"encoding/json"
	"errors"
	"net/http"
)

type apiEnvelope struct {
	Data      any    `json:"data"`
	RequestID string `json:"requestId"`
}

type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
	RequestID string `json:"requestId"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func writeAPIData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, apiEnvelope{Data: data, RequestID: newID("req_")})
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeAPIErrorDetails(w, status, code, message, nil)
}

func writeAPIErrorDetails(w http.ResponseWriter, status int, code, message string, details any) {
	var body apiErrorBody
	body.Error.Code = code
	body.Error.Message = message
	body.Error.Details = details
	body.RequestID = newID("req_")
	writeJSON(w, status, body)
}

func statusFromError(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	default:
		return http.StatusBadRequest
	}
}

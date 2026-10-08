// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
)

// ErrorCode is a machine-readable failure identifier. The set equals the
// ErrorCode enum of docs/openapi.json; contract_gate_test.go keeps them equal.
type ErrorCode string

// Error codes.
const (
	CodeUnauthorized         ErrorCode = "UNAUTHORIZED"
	CodeValidationFailed     ErrorCode = "VALIDATION_FAILED"
	CodeUnknownScopeKey      ErrorCode = "UNKNOWN_SCOPE_KEY"
	CodeUpstreamTimeout      ErrorCode = "UPSTREAM_TIMEOUT"
	CodeUpstreamUnavailable  ErrorCode = "UPSTREAM_UNAVAILABLE"
	CodeIndexUnavailable     ErrorCode = "INDEX_UNAVAILABLE"
	CodeFactTableNotFound    ErrorCode = "FACT_TABLE_NOT_FOUND"
	CodeFactTableUnavailable ErrorCode = "FACT_TABLE_UNAVAILABLE"
	CodeNotFound             ErrorCode = "NOT_FOUND"
	CodeMethodNotAllowed     ErrorCode = "METHOD_NOT_ALLOWED"
	CodeStorageDown          ErrorCode = "STORAGE_DOWN"
	CodeInternal             ErrorCode = "INTERNAL"
)

// APIError is the body of the error envelope.
type APIError struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"`
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the single error envelope. Message is diagnostic English.
func writeError(w http.ResponseWriter, status int, code ErrorCode, message string, params map[string]any) {
	writeJSON(w, status, errorEnvelope{Error: APIError{Code: code, Message: message, Params: params}})
}

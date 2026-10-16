package main

import (
	"encoding/json"
	"io"
	"net/http"
)

// writeJSON writes v as a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		logError(err)
		status, body = http.StatusInternalServerError, []byte(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError is the one way every handler sends an error: {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON reads exactly one JSON value into dst, rejecting unknown fields,
// wrong types, an empty body and trailing data. On failure it has already
// written 400 "invalid JSON body" and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// internalError logs the cause server-side and sends a generic 500.
func internalError(w http.ResponseWriter, err error) {
	logError(err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

// decodeJSONQuiet is decodeJSON without writing the error, for handlers that
// must run other checks (404, 403) before reporting a bad body.
func decodeJSONQuiet(r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst) == nil && dec.Decode(&struct{}{}) == io.EOF
}

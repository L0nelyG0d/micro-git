package main

import (
	"encoding/json"
	"errors"
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

// maxJSONBody is the largest JSON request body decodeJSON accepts (commits
// have their own, larger limit).
const maxJSONBody = 1 << 20

// decodeBody reads exactly one JSON value into dst, rejecting unknown fields,
// wrong types, an empty body and trailing data. It limits the body to
// maxJSONBody bytes.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst) == nil && dec.Decode(&struct{}{}) == io.EOF
}

// bodyTooLarge reports whether the body of r, already read by decodeBody,
// went over maxJSONBody. It reads the rest of the (bounded) body.
func bodyTooLarge(r *http.Request) bool {
	_, err := io.Copy(io.Discard, r.Body)
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// badBody writes the error for a body decodeBody rejected: 413 "request too
// large" when it was over the limit, otherwise 400 "invalid JSON body".
func badBody(w http.ResponseWriter, r *http.Request) {
	if bodyTooLarge(r) {
		writeError(w, http.StatusRequestEntityTooLarge, "request too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON body")
}

// decodeJSON is decodeBody that also writes the error (see badBody) and
// returns false on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if decodeBody(w, r, dst) {
		return true
	}
	badBody(w, r)
	return false
}

// internalError logs the cause server-side and sends a generic 500.
func internalError(w http.ResponseWriter, err error) {
	logError(err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

// decodeJSONQuiet is decodeJSON without writing the error, for handlers that
// must run other checks (404, 403) before calling badBody.
func decodeJSONQuiet(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeBody(w, r, dst)
}

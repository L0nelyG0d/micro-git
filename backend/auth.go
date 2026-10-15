package main

import (
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var usernameRE = regexp.MustCompile(`^[a-z0-9-]{3,32}$`)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// register handles POST /auth/register. See plan.md "API contracts > Auth".
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	username := strings.ToLower(req.Username)
	switch {
	case !usernameRE.MatchString(username):
		writeError(w, http.StatusBadRequest, "username must be 3-32 characters: a-z, 0-9 and -")
		return
	case username == "api":
		writeError(w, http.StatusBadRequest, "username is reserved")
		return
	case len(req.Password) < 8 || len(req.Password) > 72:
		writeError(w, http.StatusBadRequest, "password must be 8-72 bytes")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		internalError(w, err)
		return
	}
	var id int64
	err = s.DB.QueryRow(r.Context(),
		`INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id`,
		username, string(hash)).Scan(&id)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "username already taken")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, userResponse{ID: id, Username: username})
}

package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName = "session"
	sessionTTL = 7 * 24 * time.Hour
)

// User is the authenticated user the middleware puts in the request context.
type User struct {
	ID       int64
	Username string
}

type ctxKey struct{}

// UserFromContext returns the user set by requireAuth or optionalAuth.
// ok is false for an anonymous request.
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// requireAuth wraps a handler so it only runs for a logged-in user (read it
// with UserFromContext); otherwise 401 "authentication required".
func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok, err := s.authenticate(r)
		if err != nil {
			internalError(w, err)
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	}
}

// optionalAuth is for public endpoints that want to know the viewer: the
// handler always runs, and UserFromContext reports ok only when logged in.
func (s *Server) optionalAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok, err := s.authenticate(r)
		if err != nil {
			internalError(w, err)
			return
		}
		if ok {
			r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
		}
		h(w, r)
	}
}

// authenticate resolves the session cookie to an existing user. A bad,
// expired or foreign token, or a deleted user, is (zero, false, nil); only
// database failures are errors.
func (s *Server) authenticate(r *http.Request) (User, bool, error) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return User{}, false, nil
	}
	var claims jwt.RegisteredClaims
	_, err = jwt.ParseWithClaims(c.Value, &claims,
		func(*jwt.Token) (any, error) { return s.JWTSecret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(s.Now),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return User{}, false, nil
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return User{}, false, nil
	}
	u := User{ID: id}
	err = s.DB.QueryRow(r.Context(), `SELECT username FROM users WHERE id = $1`, id).Scan(&u.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

func (s *Server) issueToken(userID int64) (string, error) {
	now := s.Now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(userID, 10),
		ExpiresAt: jwt.NewNumericDate(now.Add(sessionTTL)),
	}).SignedString(s.JWTSecret)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.CookieSecure,
	})
}

// dummyHash makes an unknown user cost the same bcrypt time as a wrong password.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy password"), bcrypt.DefaultCost)

// login handles POST /auth/login.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	username := strings.ToLower(req.Username)
	var id int64
	var hash string
	err := s.DB.QueryRow(r.Context(),
		`SELECT id, password_hash FROM users WHERE lower(username) = $1`, username).Scan(&id, &hash)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		internalError(w, err)
		return
	}
	if !found {
		hash = string(dummyHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil || !found {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	token, err := s.issueToken(id)
	if err != nil {
		internalError(w, err)
		return
	}
	s.setSessionCookie(w, token, int(sessionTTL/time.Second))
	writeJSON(w, http.StatusOK, userResponse{ID: id, Username: username})
}

// logout handles POST /auth/logout; idempotent, never 401.
func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	s.setSessionCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// me handles GET /auth/me (behind requireAuth).
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	writeJSON(w, http.StatusOK, userResponse{ID: u.ID, Username: u.Username})
}

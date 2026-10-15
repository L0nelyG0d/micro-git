package main

import (
	"errors"
	"fmt"
	"strconv"
)

// Config holds process settings read from the environment.
type Config struct {
	Addr        string // listen address, ":" + PORT
	DatabaseURL string
	JWTSecret   string
	// CookieSecure adds the Secure attribute to the session cookie (COOKIE_SECURE=true).
	CookieSecure bool
}

// LoadConfig reads settings through getenv (os.Getenv in production) so tests
// can pass a fake. Later tasks add their own fields (JWT_SECRET, COOKIE_SECURE).
func LoadConfig(getenv func(string) string) (Config, error) {
	port := getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("PORT must be a number from 1 to 65535, got %q", port)
	}
	dbURL := getenv("DATABASE_URL")
	if dbURL == "" {
		return Config{}, errors.New("DATABASE_URL is not set")
	}
	secret := getenv("JWT_SECRET")
	if secret == "" {
		return Config{}, errors.New("JWT_SECRET is not set")
	}
	return Config{
		Addr:         ":" + port,
		DatabaseURL:  dbURL,
		JWTSecret:    secret,
		CookieSecure: getenv("COOKIE_SECURE") == "true",
	}, nil
}

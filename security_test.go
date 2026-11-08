package main

import (
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func TestValidateBindAddress(t *testing.T) {
    cases := []struct {
        address, token string
        allowed bool
    }{
        {"127.0.0.1:8080", "", true},
        {"localhost:8080", "", true},
        {"[::1]:8080", "", true},
        {"0.0.0.0:8080", "", false},
        {":8080", "", false},
        {"192.168.1.10:8080", "", false},
        {"[::]:8080", "", false},
        {"0.0.0.0:8080", "secret", true},
        {"invalid-address", "secret", false},
    }
    for _, tc := range cases {
        err := validateBindAddress(tc.address, tc.token)
        if (err == nil) != tc.allowed {
            t.Errorf("validateBindAddress(%q, token=%t): %v", tc.address, tc.token != "", err)
        }
    }
}

func TestTokenAuthenticationProtectsReadsAndMutations(t *testing.T) {
    handler := secureHandler(NewQueue(), "very-secret")
    cases := []struct {
        method, path, authorization string
        expected int
    }{
        {"GET", "/health", "", http.StatusOK},
        {"GET", "/stats", "", http.StatusUnauthorized},
        {"GET", "/tickets", "Bearer wrong", http.StatusUnauthorized},
        {"GET", "/export", "Basic very-secret", http.StatusUnauthorized},
        {"GET", "/tickets/search", "Bearer very-secret ", http.StatusUnauthorized},
        {"POST", "/tickets", "", http.StatusUnauthorized},
        {"POST", "/cancel", "", http.StatusUnauthorized},
        {"POST", "/transfer", "", http.StatusUnauthorized},
        {"POST", "/requeue", "", http.StatusUnauthorized},
        {"GET", "/stats", "Bearer very-secret", http.StatusOK},
        {"POST", "/tickets", "Bearer very-secret", http.StatusCreated},
        {"GET", "/export", "Bearer very-secret", http.StatusOK},
    }
    for _, tc := range cases {
        req := httptest.NewRequest(tc.method, tc.path, nil)
        if tc.authorization != "" {
            req.Header.Set("Authorization", tc.authorization)
        }
        w := httptest.NewRecorder()
        handler.ServeHTTP(w, req)
        if w.Code != tc.expected {
            t.Errorf("%s %s auth=%t: got %d expected %d: %s", tc.method, tc.path, tc.authorization != "", w.Code, tc.expected, w.Body.String())
        }
        if tc.path != "/health" && w.Header().Get("Cache-Control") != "no-store" {
            t.Errorf("%s %s missing no-store", tc.method, tc.path)
        }
        if strings.Contains(w.Body.String(), "very-secret") {
            t.Errorf("credential leaked in response")
        }
        if tc.expected == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") != "Bearer" {
            t.Errorf("missing bearer challenge")
        }
    }
}

func TestLocalDevelopmentRemainsBackwardsCompatible(t *testing.T) {
    handler := secureHandler(NewQueue(), "")
    req := httptest.NewRequest(http.MethodPost, "/tickets", nil)
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)
    if w.Code != http.StatusCreated {
        t.Fatalf("local-only mode changed: %d %s", w.Code, w.Body.String())
    }
}

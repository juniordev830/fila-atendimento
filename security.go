package main

import (
    "crypto/subtle"
    "errors"
    "net"
    "net/http"
)

// validateBindAddress prevents accidental public exposure of unauthenticated
// state-changing endpoints. Token-authenticated deployments still require TLS.
func validateBindAddress(addr, token string) error {
    host, _, err := net.SplitHostPort(addr)
    if err != nil {
        return err
    }
    if token != "" || host == "localhost" {
        return nil
    }
    ip := net.ParseIP(host)
    if ip != nil && ip.IsLoopback() {
        return nil
    }
    return errors.New("API_TOKEN obrigatorio para escutar fora de loopback")
}

// secureHandler leaves only the health probe public when API_TOKEN is set.
// The bearer credential is never included in responses or logs.
func secureHandler(q *Queue, token string) http.Handler {
    inner := newHandler(q)
    if token == "" {
        return inner
    }
    expected := []byte("Bearer " + token)
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/health" && r.Method == http.MethodGet {
            inner.ServeHTTP(w, r)
            return
        }
        w.Header().Set("Cache-Control", "no-store")
        values := r.Header.Values("Authorization")
        // Reject repeated credentials: intermediaries may interpret them differently.
        if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), expected) != 1 {
            w.Header().Set("WWW-Authenticate", "Bearer")
            respond(w, http.StatusUnauthorized, map[string]string{"erro": "credencial obrigatoria"})
            return
        }
        inner.ServeHTTP(w, r)
    })
}

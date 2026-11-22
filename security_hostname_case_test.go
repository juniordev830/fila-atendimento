package main

import "testing"

func TestLoopbackHostnameCaseInsensitive(t *testing.T) {
    for _, address := range []string{"LOCALHOST:8080", "LocalHost:8080", "localhost:8080"} {
        if err := validateBindAddress(address, ""); err != nil {
            t.Errorf("expected loopback %s without token, got %v", address, err)
        }
    }
    if err := validateBindAddress("public.example:8080", ""); err == nil {
        t.Fatal("external host without token must remain prohibited")
    }
}

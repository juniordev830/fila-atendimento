package main

import (
    "encoding/json"
    "errors"
    "net/http"
    "reflect"
    "strings"
    "testing"
)

func TestDeskValidationPreservesQueueOnInvalidSingleOperations(t *testing.T) {
    invalid := []string{
        "", " \t ", "guiche\ninvalido", "guiche\x00invalido",
        strings.Repeat("A", 65), strings.Repeat("á", 65),
    }
    for _, desk := range invalid {
        q := NewQueue()
        q.Issue()
        before := q.Snapshot()
        if _, err := q.Call(desk); !errors.Is(err, ErrInvalidDesk) {
            t.Errorf("Call(%q) expected ErrInvalidDesk; got %v", desk, err)
        }
        if !reflect.DeepEqual(q.Snapshot(), before) {
            t.Fatalf("invalid Call(%q) modified queue", desk)
        }
        if _, err := q.Call("guiche-A"); err != nil {
            t.Fatalf("valid Call after invalid attempt: %v", err)
        }
        called := q.Snapshot()
        for _, pair := range [][2]string{{"guiche-A", desk}, {desk, "guiche-B"}} {
            if _, err := q.Transfer(1, pair[0], pair[1]); !errors.Is(err, ErrInvalidDesk) {
                t.Errorf("Transfer(%q, %q) expected invalid desk; got %v", pair[0], pair[1], err)
            }
            if !reflect.DeepEqual(q.Snapshot(), called) {
                t.Fatalf("invalid transfer modified queue: %q -> %q", pair[0], pair[1])
            }
        }
    }
}

func TestUnicodeDeskBoundaryRemainsSupported(t *testing.T) {
    q := NewQueue()
    q.Issue()
    valid := strings.Repeat("á", 64)
    if ticket, err := q.Call("  " + valid + "  "); err != nil || ticket.Desk != valid {
        t.Fatalf("64 Unicode characters should be accepted: %+v, %v", ticket, err)
    }
    if ticket, err := q.Transfer(1, valid, "guiche-2"); err != nil || ticket.Desk != "guiche-2" {
        t.Fatalf("transfer from valid Unicode desk failed: %+v, %v", ticket, err)
    }
}

func TestHTTPRejectsInvalidDeskWith400AndNoSideEffects(t *testing.T) {
    q := NewQueue()
    q.Issue()
    handler := newHandler(q)
    invalid := []string{"A\nB", strings.Repeat("c", 65)}
    for _, desk := range invalid {
        data, _ := json.Marshal(map[string]string{"desk": desk})
        request(t, handler, http.MethodPost, "/call", string(data), http.StatusBadRequest)
        if got := q.Snapshot()[0]; got.Status != "waiting" || got.Desk != "" {
            t.Fatalf("invalid HTTP call mutated ticket: %+v", got)
        }
    }

    request(t, handler, http.MethodPost, "/call", `{"desk":"A"}`, http.StatusOK)
    for _, desk := range invalid {
        data, _ := json.Marshal(map[string]any{
            "id": 1, "source": "A", "destination": desk,
        })
        request(t, handler, http.MethodPost, "/transfer", string(data), http.StatusBadRequest)
        if got := q.Snapshot()[0]; got.Status != "called" || got.Desk != "A" {
            t.Fatalf("invalid HTTP transfer mutated ticket: %+v", got)
        }
    }
}

func TestRestoreRejectsDesksOutsideRuntimeValidation(t *testing.T) {
    for _, tc := range []struct {
        status, desk string
    }{
        {"called", strings.Repeat("A", 65)},
        {"completed", "A\nB"},
        {"absent", " \t "},
        {"called", " A "},
        {"waiting", "A"},
        {"cancelled", "A"},
    } {
        snapshot, err := json.Marshal(map[string]any{
            "version": 1,
            "next_id": 2,
            "tickets": []Ticket{{ID: 1, Status: tc.status, Desk: tc.desk}},
        })
        if err != nil {
            t.Fatal(err)
        }
        if _, err := RestoreQueue(snapshot); err == nil {
            t.Errorf("restored invalid %s desk %q", tc.status, tc.desk)
        }
    }
}

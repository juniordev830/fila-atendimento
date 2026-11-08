package main

import (
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "net/http/httptest"
    "sync"
    "testing"
)

func TestCancelOnlyWaitingAndPreservesSequence(t *testing.T) {
    q := NewQueue()
    a, b, c := q.Issue(), q.Issue(), q.Issue()
    if _, err := q.Cancel(0); !errors.Is(err, ErrTicketMissing) {
        t.Fatalf("unknown ticket: %v", err)
    }
    cancelled, err := q.Cancel(b.ID)
    if err != nil || cancelled.Status != "cancelled" {
        t.Fatalf("cancel failed: %+v %v", cancelled, err)
    }
    if _, err := q.Cancel(b.ID); !errors.Is(err, ErrNotWaiting) {
        t.Fatalf("double cancel: %v", err)
    }
    first, err := q.Call("A")
    if err != nil || first.ID != a.ID {
        t.Fatalf("first ticket changed: %+v %v", first, err)
    }
    second, err := q.Call("B")
    if err != nil || second.ID != c.ID {
        t.Fatalf("cancelled ticket called: %+v %v", second, err)
    }
    if q.Issue().ID != 4 {
        t.Fatal("IDs must not be recycled after cancellation")
    }
    if _, err := q.Cancel(a.ID); !errors.Is(err, ErrNotWaiting) {
        t.Fatalf("called ticket cancelled: %v", err)
    }
}

func TestTransferValidatesDeskOwnershipAndCapacity(t *testing.T) {
    q := NewQueue()
    a, b := q.Issue(), q.Issue()
    if _, err := q.Transfer(a.ID, "A", "B"); !errors.Is(err, ErrNotCalled) {
        t.Fatalf("transfer of waiting ticket: %v", err)
    }
    if _, err := q.Call("A"); err != nil { t.Fatal(err) }
    if _, err := q.Call("B"); err != nil { t.Fatal(err) }
    cases := []struct {
        source, target string
        id int
        expected error
    }{
        {"A", "A", a.ID, ErrSameDesk},
        {"A", "B", a.ID, ErrDeskBusy},
        {"wrong", "C", a.ID, ErrDeskMismatch},
        {"A", "C", 999, ErrTicketMissing},
        {"", "C", a.ID, nil},
    }
    for _, tc := range cases {
        _, err := q.Transfer(tc.id, tc.source, tc.target)
        if tc.expected == nil {
            if err == nil { t.Fatalf("missing source accepted") }
        } else if !errors.Is(err, tc.expected) {
            t.Fatalf("transfer %+v: got %v", tc, err)
        }
    }
    moved, err := q.Transfer(a.ID, " A ", " C ")
    if err != nil || moved.Desk != "C" {
        t.Fatalf("valid transfer: %+v %v", moved, err)
    }
    if _, err := q.Resolve(a.ID, "completed"); err != nil { t.Fatal(err) }
    if _, err := q.Transfer(a.ID, "C", "D"); !errors.Is(err, ErrNotCalled) {
        t.Fatalf("transfer after completion: %v", err)
    }
    if _, err := q.Transfer(b.ID, "B", "A"); err != nil {
        t.Fatalf("freed source desk must be available: %v", err)
    }
}

func TestRequeuePreservesArrivalOrderAndReleasesDesk(t *testing.T) {
    q := NewQueue()
    a, b, c := q.Issue(), q.Issue(), q.Issue()
    if _, err := q.Requeue(a.ID); !errors.Is(err, ErrNotCalled) {
        t.Fatalf("waiting ticket requeued: %v", err)
    }
    if _, err := q.Call("A"); err != nil { t.Fatal(err) }
    if _, err := q.Call("B"); err != nil { t.Fatal(err) }
    back, err := q.Requeue(a.ID)
    if err != nil || back.Status != "waiting" || back.Desk != "" {
        t.Fatalf("requeue: %+v %v", back, err)
    }
    if pos, err := q.Position(a.ID); err != nil || pos != 1 {
        t.Fatalf("position: %d %v", pos, err)
    }
    if pos, err := q.Position(c.ID); err != nil || pos != 2 {
        t.Fatalf("position: %d %v", pos, err)
    }
    if _, err := q.Position(b.ID); !errors.Is(err, ErrNotWaiting) {
        t.Fatalf("called ticket has position: %v", err)
    }
    if _, err := q.Position(999); !errors.Is(err, ErrTicketMissing) {
        t.Fatalf("missing ticket has position: %v", err)
    }
    next, err := q.Call("A")
    if err != nil || next.ID != a.ID {
        t.Fatalf("requeued ticket not called in original order: %+v %v", next, err)
    }
    if _, err := q.Requeue(a.ID); err != nil { t.Fatal(err) }
    if _, err := q.Resolve(b.ID, "absent"); err != nil { t.Fatal(err) }
    if _, err := q.Requeue(b.ID); !errors.Is(err, ErrNotCalled) {
        t.Fatalf("absent ticket requeued: %v", err)
    }
}

func TestStatsAndFilteredPagination(t *testing.T) {
    q := NewQueue()
    for i := 0; i < 8; i++ { q.Issue() }
    if _, err := q.Call("B"); err != nil { t.Fatal(err) }
    if _, err := q.Call("A"); err != nil { t.Fatal(err) }
    if _, err := q.Resolve(1, "completed"); err != nil { t.Fatal(err) }
    if _, err := q.Cancel(3); err != nil { t.Fatal(err) }
    stats := q.Stats()
    counts := stats["counts"].(map[string]int)
    if counts["waiting"] != 5 || counts["called"] != 1 || counts["completed"] != 1 || counts["cancelled"] != 1 || stats["next_id"] != 9 {
        t.Fatalf("wrong stats: %#v", stats)
    }
    page, err := q.Page("waiting", 1, 2)
    if err != nil { t.Fatal(err) }
    items := page["items"].([]Ticket)
    if page["total"] != 5 || len(items) != 2 || items[0].ID != 5 || items[1].ID != 6 {
        t.Fatalf("wrong page: %#v", page)
    }
    empty, err := q.Page("waiting", 99, 10)
    if err != nil || len(empty["items"].([]Ticket)) != 0 || empty["total"] != 5 {
        t.Fatalf("empty page: %#v %v", empty, err)
    }
    for _, tc := range []struct{ status string; offset, limit int; expected error }{
        {"unknown", 0, 10, ErrInvalidStatus},
        {"waiting", -1, 10, ErrInvalidPage},
        {"", 0, 0, ErrInvalidPage},
        {"", 0, 101, ErrInvalidPage},
    } {
        _, err := q.Page(tc.status, tc.offset, tc.limit)
        if !errors.Is(err, tc.expected) { t.Fatalf("page %+v: %v", tc, err) }
    }
}

func TestSnapshotRoundTripPreservesSequenceAndActiveDesks(t *testing.T) {
    q := NewQueue()
    q.Issue()
    q.Issue()
    q.Issue()
    if _, err := q.Call("desk-1"); err != nil { t.Fatal(err) }
    if _, err := q.Cancel(2); err != nil { t.Fatal(err) }
    snapshot, err := q.ExportSnapshot()
    if err != nil { t.Fatal(err) }
    restored, err := RestoreQueue(snapshot)
    if err != nil { t.Fatal(err) }
    if _, err := restored.Call("desk-1"); !errors.Is(err, ErrDeskBusy) {
        t.Fatalf("restored desk ownership: %v", err)
    }
    next, err := restored.Call("desk-2")
    if err != nil || next.ID != 3 { t.Fatalf("restored order: %+v %v", next, err) }
    if issued := restored.Issue(); issued.ID != 4 { t.Fatalf("restored sequence: %+v", issued) }
    if q.Issue().ID != 4 { t.Fatal("restore should not mutate original queue") }
}

func TestRestoreRejectsCorruptedSnapshots(t *testing.T) {
    cases := []string{
        "",
        "null",
        "{}",
        `{"version":2,"next_id":2,"tickets":[]}`,
        `{"version":1,"next_id":0,"tickets":[]}`,
        `{"version":1,"next_id":2,"tickets":[{"id":2,"status":"waiting"}]}`,
        `{"version":1,"next_id":3,"tickets":[{"id":2,"status":"waiting"},{"id":1,"status":"waiting"}]}`,
        `{"version":1,"next_id":3,"tickets":[{"id":1,"status":"unknown"}]}`,
        `{"version":1,"next_id":3,"tickets":[{"id":1,"status":"called"}]}`,
        `{"version":1,"next_id":3,"tickets":[{"id":1,"status":"waiting","desk":"A"}]}`,
        `{"version":1,"next_id":3,"tickets":[{"id":1,"status":"called","desk":"A"},{"id":2,"status":"called","desk":"A"}]}`,
        `{"version":1,"next_id":2,"tickets":[],"extra":true}`,
        `{"version":1,"next_id":2,"tickets":[]} {"version":1}`,
    }
    for i, data := range cases {
        if q, err := RestoreQueue([]byte(data)); err == nil {
            t.Fatalf("case %d accepted: %#v", i, q)
        }
    }
    if _, err := RestoreQueue(bytes.Repeat([]byte(" "), 5*1024*1024+1)); err == nil {
        t.Fatal("oversized snapshot accepted")
    }
}

func TestConcurrentCancelsAndCallsAreConsistent(t *testing.T) {
    q := NewQueue()
    for i := 0; i < 120; i++ { q.Issue() }
    var wg sync.WaitGroup
    for i := 1; i <= 120; i++ {
        wg.Add(2)
        go func(id int) {
            defer wg.Done()
            _, _ = q.Cancel(id)
        }(i)
        go func(id int) {
            defer wg.Done()
            _, _ = q.Call(fmt.Sprintf("desk-%d", id))
        }(i)
    }
    wg.Wait()
    tickets := q.Snapshot()
    seen := map[int]bool{}
    desks := map[string]bool{}
    for _, ticket := range tickets {
        if seen[ticket.ID] { t.Fatalf("duplicate ticket %d", ticket.ID) }
        seen[ticket.ID] = true
        if ticket.Status == "called" {
            if desks[ticket.Desk] { t.Fatalf("duplicate active desk %s", ticket.Desk) }
            desks[ticket.Desk] = true
        } else if ticket.Status != "cancelled" {
            t.Fatalf("unexpected state after competing call/cancel: %+v", ticket)
        }
    }
    if len(seen) != 120 { t.Fatalf("lost tickets: %d", len(seen)) }
}

func TestAdvancedHTTPRoutesAndValidation(t *testing.T) {
    h := newHandler(NewQueue())
    request(t, h, "POST", "/tickets", "", http.StatusCreated)
    request(t, h, "POST", "/tickets", "", http.StatusCreated)
    request(t, h, "POST", "/tickets", "", http.StatusCreated)
    stats := request(t, h, "GET", "/stats", "", http.StatusOK)
    if stats["total"] != float64(3) { t.Fatalf("stats: %v", stats) }
    position := request(t, h, "GET", "/tickets/position?id=2", "", http.StatusOK)
    if position["position"] != float64(2) { t.Fatalf("position: %v", position) }
    page := request(t, h, "GET", "/tickets/search?status=waiting&limit=2&offset=1", "", http.StatusOK)
    if page["total"] != float64(3) || len(page["items"].([]any)) != 2 {
        t.Fatalf("pagination: %v", page)
    }
    request(t, h, "GET", "/tickets/search?limit=101", "", http.StatusBadRequest)
    request(t, h, "GET", "/tickets/search?limit=2&limit=3", "", http.StatusBadRequest)
    request(t, h, "GET", "/tickets/search?unexpected=1", "", http.StatusBadRequest)
    request(t, h, "GET", "/tickets/search?status=invalid", "", http.StatusBadRequest)
    request(t, h, "GET", "/tickets/position?id=abc", "", http.StatusBadRequest)
    request(t, h, "POST", "/cancel", `{"id":2}`, http.StatusOK)
    request(t, h, "POST", "/cancel", `{"id":2}`, http.StatusConflict)
    request(t, h, "POST", "/cancel", `{"id":0}`, http.StatusBadRequest)
    request(t, h, "POST", "/cancel", `{"id":1,"admin":true}`, http.StatusBadRequest)
    request(t, h, "POST", "/call", `{"desk":"A"}`, http.StatusOK)
    request(t, h, "POST", "/transfer", `{"id":1,"source":"A","destination":"B"}`, http.StatusOK)
    request(t, h, "POST", "/transfer", `{"id":1,"source":"A","destination":"C"}`, http.StatusConflict)
    request(t, h, "POST", "/requeue", `{"id":1}`, http.StatusOK)
    request(t, h, "POST", "/requeue", `{"id":1}`, http.StatusConflict)
    request(t, h, "GET", "/tickets/position?id=1", "", http.StatusOK)
    request(t, h, "PUT", "/stats", "", http.StatusMethodNotAllowed)
    request(t, h, "POST", "/export", "", http.StatusMethodNotAllowed)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, httptest.NewRequest("GET", "/export", nil))
    if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
        t.Fatalf("export headers: %d %v", w.Code, w.Header())
    }
    var export map[string]any
    if err := json.Unmarshal(w.Body.Bytes(), &export); err != nil { t.Fatal(err) }
    if export["version"] != float64(1) || export["next_id"] != float64(4) {
        t.Fatalf("export metadata: %v", export)
    }
    if _, err := RestoreQueue(w.Body.Bytes()); err != nil {
        t.Fatalf("export is not restorable: %v", err)
    }
}

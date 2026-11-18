package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"strings"
	"testing"
)

func TestTicketLifecycleAndDeskInvariants(t *testing.T) {
	q := NewQueue()
	first, second := q.Issue(), q.Issue()
	if first.ID != 1 || second.ID != 2 { t.Fatal("senhas devem ser sequenciais") }
	called, err := q.Call("guiche-1")
	if err != nil || called.ID != first.ID { t.Fatal("chamada nao respeita ordem", called, err) }
	if _, err := q.Call("guiche-1"); err != ErrDeskBusy { t.Fatalf("guiche ocupado: %v", err) }
	next, err := q.Call("guiche-2")
	if err != nil || next.ID != second.ID { t.Fatal("segunda senha incorreta", next, err) }
	if _, err := q.Call("guiche-3"); err != ErrNoWaiting { t.Fatalf("fila vazia: %v", err) }
	if _, err := q.Resolve(999, "completed"); err != ErrNotCalled { t.Fatalf("senha inexistente: %v", err) }
	if _, err := q.Resolve(first.ID, "other"); err != ErrBadOutcome { t.Fatalf("status invalido: %v", err) }
	if resolved, err := q.Resolve(first.ID, "absent"); err != nil || resolved.Status != "absent" { t.Fatal(resolved, err) }
	if _, err := q.Resolve(first.ID, "completed"); err != ErrNotCalled { t.Fatalf("finalizacao duplicada: %v", err) }
	if _, err := q.Call("guiche-1"); err != ErrNoWaiting { t.Fatalf("guiche liberado, mas sem senhas: %v", err) }
}

func TestConcurrentCallsNeverReuseTicket(t *testing.T) {
	q := NewQueue()
	for i := 0; i < 40; i++ { q.Issue() }
	var wg sync.WaitGroup
	results := make(chan int, 80)
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if ticket, err := q.Call("guiche-" + string(rune('A'+n))); err == nil {
				results <- ticket.ID
			}
		}(i)
	}
	wg.Wait()
	close(results)
	seen := map[int]bool{}
	for id := range results {
		if seen[id] { t.Fatalf("senha chamada duas vezes: %d", id) }
		seen[id] = true
	}
	if len(seen) != 40 { t.Fatalf("esperadas 40 chamadas unicas, recebidas %d", len(seen)) }
}

func request(t *testing.T, h http.Handler, method, path, payload string, status int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewBufferString(payload)))
	if w.Code != status { t.Fatalf("%s %s: %d, esperado %d, resposta %s", method, path, w.Code, status, w.Body.String()) }
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" { t.Fatalf("Content-Type: %s", ct) }
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil { t.Fatal(err) }
	return data
}

func TestHTTPAPI(t *testing.T) {
	h := newHandler(NewQueue())
	issued := request(t, h, "POST", "/tickets", "", http.StatusCreated)
	if issued["id"] != float64(1) { t.Fatal(issued) }
	request(t, h, "POST", "/call", `{"desk":"A"}`, http.StatusOK)
	request(t, h, "POST", "/call", `{"desk":"A"}`, http.StatusConflict)
	request(t, h, "POST", "/resolve", `{"id":1,"status":"completed"}`, http.StatusOK)
	request(t, h, "POST", "/resolve", `{"id":1,"status":"completed"}`, http.StatusConflict)
	request(t, h, "POST", "/call", `{"desk":""}`, http.StatusBadRequest)
	request(t, h, "POST", "/call", `{"desk":"A","admin":true}`, http.StatusBadRequest)
	request(t, h, "GET", "/health", "", http.StatusOK)
	request(t, h, "PUT", "/tickets", "", http.StatusMethodNotAllowed)
}

func TestOversizedJSONCannotMutateQueue(t *testing.T) {
	q := NewQueue()
	issued := q.Issue()
	h := newHandler(q)
	// The legacy capped decoder could accept a small JSON object plus
	// extra trailing spaces because the reader returned a synthetic EOF.
	large := `{"desk":"A"}` + strings.Repeat(" ", 2100)
	request(t, h, "POST", "/call", large, http.StatusBadRequest)
	items := q.Snapshot()
	if len(items) != 1 || items[0].ID != issued.ID || items[0].Status != "waiting" {
		t.Fatalf("oversized request modified queue: %+v", items)
	}

	// An in-bound request still works and a second object is rejected.
	request(t, h, "POST", "/call", `{"desk":"A"}`, http.StatusOK)
	request(t, h, "POST", "/resolve",
		`{"id":1,"status":"completed"} {"id":1,"status":"absent"}`,
		http.StatusBadRequest)
}

func TestJSONBodyLimitAtExactBoundary(t *testing.T) {
    const maxBytes = 2048
    payload := `{"desk":"B"}`
    exactlyAllowed := payload + strings.Repeat(" ", maxBytes-len(payload))
    if len(exactlyAllowed) != maxBytes {
        t.Fatal("invalid boundary fixture")
    }

    allowed := NewQueue()
    allowed.Issue()
    request(t, newHandler(allowed), "POST", "/call", exactlyAllowed, http.StatusOK)
    called := allowed.Snapshot()
    if len(called) != 1 || called[0].Status != "called" || called[0].Desk != "B" {
        t.Fatalf("exactly 2048 bytes should be accepted: %+v", called)
    }

    rejected := NewQueue()
    rejected.Issue()
    justTooLarge := exactlyAllowed + " "
    if len(justTooLarge) != maxBytes+1 {
        t.Fatal("invalid overflow fixture")
    }
    request(t, newHandler(rejected), "POST", "/call", justTooLarge, http.StatusBadRequest)
    after := rejected.Snapshot()
    if len(after) != 1 || after[0].Status != "waiting" || after[0].Desk != "" {
        t.Fatalf("oversized request must not mutate queue: %+v", after)
    }
}

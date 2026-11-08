package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
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

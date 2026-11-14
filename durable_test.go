package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func persistentRequest(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, req)
	return reply
}

func TestDurableQueueRecoversTransactionsAndNextID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	handler := durableHandler(q, "", path)

	for i := 0; i < 2; i++ {
		if got := persistentRequest(handler, http.MethodPost, "/tickets", "", "").Code; got != 201 {
			t.Fatalf("issue HTTP status = %d", got)
		}
	}
	if got := persistentRequest(handler, http.MethodPost, "/call", `{"desk":"A"}`, "").Code; got != 200 {
		t.Fatalf("call HTTP status = %d", got)
	}
	if got := persistentRequest(handler, http.MethodPost, "/resolve", `{"id":1,"status":"completed"}`, "").Code; got != 200 {
		t.Fatalf("resolve HTTP status = %d", got)
	}

	reopened, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	if reopened.nextID != 3 || reopened.Snapshot()[0].Status != "completed" {
		t.Fatalf("state was not recovered: %+v nextID=%d", reopened.Snapshot(), reopened.nextID)
	}
	if issued := reopened.Issue(); issued.ID != 3 {
		t.Fatalf("sequence restarted at %d", issued.ID)
	}
}

func TestDurableQueueRejectsCorruptStateInsteadOfRestarting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	if err := os.WriteFile(path, []byte("invalid json"), 0600); err != nil { t.Fatal(err) }
	if _, err := openDurableQueue(path); err == nil {
		t.Fatal("corrupted persisted state must stop startup")
	}
	if bytes, err := os.ReadFile(path); err != nil || string(bytes) != "invalid json" {
		t.Fatalf("corrupted state was overwritten: %v", err)
	}
}

func TestDurableQueueDoesNotPublishFailedWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.json")
	q, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	handler := durableHandler(q, "", path)

	if got := persistentRequest(handler, http.MethodPost, "/call", `{"desk":"A"}`, "").Code; got != 409 {
		t.Fatalf("expected conflict without a ticket, got %d", got)
	}
	// The queue is held in memory, but the backing directory disappeared.
	// A failed disk commit must not acknowledge or publish the ticket.
	if err := os.Remove(path); err != nil { t.Fatal(err) }
	if err := os.Remove(dir); err != nil { t.Fatal(err) }
	if got := persistentRequest(handler, http.MethodPost, "/tickets", "", "").Code; got != 503 {
		t.Fatalf("expected persistence failure 503, got %d", got)
	}
	if q.nextID != 1 || len(q.Snapshot()) != 0 {
		t.Fatalf("failed commit changed the live queue: %+v", q.Snapshot())
	}
}

func TestDurableQueueUnauthorizedWritesAndDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	handler := durableHandler(q, "secure-test-token", path)

	if got := persistentRequest(handler, http.MethodPost, "/tickets", "", "").Code; got != 401 {
		t.Fatalf("anonymous write status = %d", got)
	}
	reply := persistentRequest(handler, http.MethodPost, "/tickets/batch", `{"dry_run":true,"actions":[{"op":"issue"}]}`, "secure-test-token")
	if reply.Code != 200 { t.Fatalf("dry-run HTTP status = %d, body=%s", reply.Code, reply.Body.String()) }
	if q.nextID != 1 || len(q.Snapshot()) != 0 {
		t.Fatal("anonymous request or dry-run mutated the queue")
	}
	reopened, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	if reopened.nextID != 1 { t.Fatal("dry-run consumed an ID on disk") }
}

func TestDurableQueueConcurrentRequestsHaveUniqueIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	handler := durableHandler(q, "", path)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if reply := persistentRequest(handler, http.MethodPost, "/tickets", "", ""); reply.Code != 201 {
				t.Errorf("issue status = %d", reply.Code)
			}
		}()
	}
	wg.Wait()

	recovered, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	if recovered.nextID != 51 || len(recovered.Snapshot()) != 50 {
		t.Fatalf("concurrent queue lost tickets: nextID=%d count=%d", recovered.nextID, len(recovered.Snapshot()))
	}
	for i, ticket := range recovered.Snapshot() {
		if ticket.ID != i+1 { t.Fatalf("non-sequential ticket: %+v", ticket) }
	}
}

func TestDurableQueuePreventsDoubleCallAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	handler := durableHandler(q, "", path)
	persistentRequest(handler, http.MethodPost, "/tickets", "", "")
	persistentRequest(handler, http.MethodPost, "/tickets", "", "")

	var wg sync.WaitGroup
	successes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reply := persistentRequest(handler, http.MethodPost, "/call", `{"desk":"A"}`, "")
			successes <- reply.Code
		}()
	}
	wg.Wait()
	close(successes)
	ok, conflicts := 0, 0
	for code := range successes {
		if code == 200 { ok++ }
		if code == 409 { conflicts++ }
	}
	if ok != 1 || conflicts != 1 { t.Fatalf("double call: success=%d conflict=%d", ok, conflicts) }

	recovered, err := openDurableQueue(path)
	if err != nil { t.Fatal(err) }
	if recovered.Snapshot()[0].Status != "called" || recovered.Snapshot()[1].Status != "waiting" {
		t.Fatalf("invalid recovered call state: %+v", recovered.Snapshot())
	}
}

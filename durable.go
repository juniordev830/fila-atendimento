package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
)

// openDurableQueue restores the last committed state. A corrupt or unreadable
// snapshot is fatal: silently starting at ticket 1 could issue duplicate IDs.
func openDurableQueue(path string) (*Queue, error) {
	if path == "" {
		return nil, errors.New("QUEUE_FILE obrigatorio para o modo duravel")
	}
	queue, err := LoadSnapshotFile(path)
	if err == nil {
		return queue, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("recuperar fila persistida: %w", err)
	}
	queue = NewQueue()
	if err := queue.SaveSnapshotFile(path); err != nil {
		return nil, fmt.Errorf("inicializar fila persistida: %w", err)
	}
	return queue, nil
}

// durableHandler stages every POST on a private copy. It publishes the state
// only after the snapshot was atomically written and synced. The write lock
// serializes concurrent requests (including batch operations) in one process.
// Run a single server instance per QUEUE_FILE; cross-process locking is not
// implemented.
func durableHandler(queue *Queue, token, path string) http.Handler {
	var writes sync.Mutex
	readHandler := secureHandler(queue, token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			readHandler.ServeHTTP(w, r)
			return
		}
		writes.Lock()
		defer writes.Unlock()

		before, err := queue.ExportSnapshot()
		if err != nil {
			respond(w, http.StatusServiceUnavailable, map[string]string{"erro": "fila indisponivel"})
			return
		}
		staged, err := RestoreQueue(before)
		if err != nil {
			respond(w, http.StatusServiceUnavailable, map[string]string{"erro": "estado da fila invalido"})
			return
		}

		recorder := httptest.NewRecorder()
		secureHandler(staged, token).ServeHTTP(recorder, r)
		if recorder.Code >= 200 && recorder.Code < 300 {
			if err := staged.SaveSnapshotFile(path); err != nil {
				respond(w, http.StatusServiceUnavailable, map[string]string{"erro": "falha ao persistir a operacao"})
				return
			}
			queue.mu.Lock()
			queue.nextID = staged.nextID
			queue.items = append([]Ticket(nil), staged.items...)
			queue.mu.Unlock()
		}

		for key, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	})
}

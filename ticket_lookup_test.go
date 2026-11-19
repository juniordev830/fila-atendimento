package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTicketLookupReturnsCurrentState(t *testing.T) {
	queue := NewQueue()
	ticket := queue.Issue()
	if _, err := queue.Call("A"); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(queue)
	for path, status := range map[string]int{
		"/tickets/1": http.StatusOK, "/tickets/2": http.StatusNotFound,
		"/tickets/01": http.StatusBadRequest, "/tickets/x": http.StatusBadRequest,
	} {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, path, nil))
		if result.Code != status {
			t.Fatalf("%s: expected status %d, got %d", path, status, result.Code)
		}
		if status == http.StatusOK {
			var got Ticket
			if err := json.Unmarshal(result.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.ID != ticket.ID || got.Status != "called" || got.Desk != "A" {
				t.Fatalf("wrong ticket: %+v", got)
			}
		}
	}
}

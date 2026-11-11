package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

type Ticket struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	Desk   string `json:"desk,omitempty"`
}

type Queue struct {
	mu     sync.Mutex
	nextID int
	items  []Ticket
}

func NewQueue() *Queue { return &Queue{nextID: 1} }

func (q *Queue) Issue() Ticket {
	q.mu.Lock()
	defer q.mu.Unlock()
	ticket := Ticket{ID: q.nextID, Status: "waiting"}
	q.items = append(q.items, ticket)
	q.nextID++
	return ticket
}

var (
	ErrNoWaiting  = errors.New("nenhuma senha aguardando")
	ErrInvalidDesk = errors.New("guiche invalido: use ate 64 caracteres sem controles")
	ErrDeskBusy   = errors.New("guiche ja possui atendimento ativo")
	ErrNotCalled  = errors.New("senha nao esta em atendimento")
	ErrBadOutcome = errors.New("resultado deve ser completed ou absent")
)

func (q *Queue) Call(desk string) (Ticket, error) {
	desk = strings.TrimSpace(desk)
	if !validDeskName(desk) {
		return Ticket{}, ErrInvalidDesk
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, ticket := range q.items {
		if ticket.Status == "called" && ticket.Desk == desk {
			return Ticket{}, ErrDeskBusy
		}
	}
	for i := range q.items {
		if q.items[i].Status == "waiting" {
			q.items[i].Status = "called"
			q.items[i].Desk = desk
			return q.items[i], nil
		}
	}
	return Ticket{}, ErrNoWaiting
}

func (q *Queue) Resolve(id int, outcome string) (Ticket, error) {
	if outcome != "completed" && outcome != "absent" {
		return Ticket{}, ErrBadOutcome
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.items {
		if q.items[i].ID == id {
			if q.items[i].Status != "called" {
				return Ticket{}, ErrNotCalled
			}
			q.items[i].Status = outcome
			return q.items[i], nil
		}
	}
	return Ticket{}, ErrNotCalled
}

func (q *Queue) Snapshot() []Ticket {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := make([]Ticket, len(q.items))
	copy(items, q.items)
	return items
}

func readObject(r *http.Request, out any) error {
	defer r.Body.Close()
	// Read one byte beyond the limit to distinguish oversized JSON from EOF.
	// A capped decoder alone may mistake truncated trailing content for EOF.
	body, err := io.ReadAll(io.LimitReader(r.Body, 2049))
	if err != nil {
		return err
	}
	if len(body) > 2048 {
		return errors.New("JSON excede 2048 bytes")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("esperado um unico objeto JSON")
	}
	return nil
}

func respond(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func badMethod(w http.ResponseWriter) {
	respond(w, http.StatusMethodNotAllowed, map[string]string{"erro": "metodo nao permitido"})
}

func newHandler(q *Queue) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { badMethod(w); return }
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/tickets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			respond(w, http.StatusOK, q.Snapshot())
		case http.MethodPost:
			respond(w, http.StatusCreated, q.Issue())
		default:
			badMethod(w)
		}
	})
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { badMethod(w); return }
		var input struct { Desk string `json:"desk"` }
		if err := readObject(r, &input); err != nil {
			respond(w, http.StatusBadRequest, map[string]string{"erro": "JSON invalido"}); return
		}
		ticket, err := q.Call(input.Desk)
		if err != nil {
			code := http.StatusConflict
			if errors.Is(err, ErrInvalidDesk) { code = http.StatusBadRequest }
			respond(w, code, map[string]string{"erro": err.Error()}); return
		}
		respond(w, http.StatusOK, ticket)
	})
	mux.HandleFunc("/resolve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { badMethod(w); return }
		var input struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
		}
		if err := readObject(r, &input); err != nil {
			respond(w, http.StatusBadRequest, map[string]string{"erro": "JSON invalido"}); return
		}
		ticket, err := q.Resolve(input.ID, input.Status)
		if err != nil {
			code := http.StatusConflict
			if errors.Is(err, ErrBadOutcome) || input.ID <= 0 { code = http.StatusBadRequest }
			respond(w, code, map[string]string{"erro": err.Error()}); return
		}
		respond(w, http.StatusOK, ticket)
	})
	return withAdvancedRoutes(q, mux)
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" { addr = "127.0.0.1:8080" }
	fmt.Printf("Fila de Atendimento em http://%s\n", addr)
	token := os.Getenv("API_TOKEN")
	if err := validateBindAddress(addr, token); err != nil { log.Fatal(err) }
	log.Fatal(http.ListenAndServe(addr, secureHandler(NewQueue(), token)))
}

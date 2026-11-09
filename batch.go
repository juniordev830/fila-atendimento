package main

import (
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "strings"
    "unicode"
)

const (
    maxBatchActions = 100
    maxBatchBody = 32 * 1024
    maxDeskRunes = 64
)

var (
    ErrEmptyBatch = errors.New("lote deve conter entre 1 e 100 operacoes")
    ErrInvalidBatchAction = errors.New("operacao ou campos do lote invalidos")
    ErrBatchIDOverflow = errors.New("limite de identificadores de senha atingido")
)

// BatchAction represents one operation in a transaction. Fields not used by
// an operation must be omitted to prevent ambiguous or misleading requests.
type BatchAction struct {
    Op string `json:"op"`
    ID int `json:"id,omitempty"`
    Desk string `json:"desk,omitempty"`
    Source string `json:"source,omitempty"`
    Destination string `json:"destination,omitempty"`
    Status string `json:"status,omitempty"`
}

type BatchRequest struct {
    Actions []BatchAction `json:"actions"`
    DryRun bool `json:"dry_run"`
}

type BatchResult struct {
    DryRun bool `json:"dry_run"`
    Applied int `json:"applied"`
    Tickets []Ticket `json:"tickets"`
    NextID int `json:"next_id"`
    Counts map[string]int `json:"counts"`
}

// BatchError pinpoints the first failed operation. No state is committed on
// error, including IDs issued earlier in the same request.
type BatchError struct {
    Index int
    Cause error
}

func (e *BatchError) Error() string {
    return fmt.Sprintf("operacao %d: %v", e.Index+1, e.Cause)
}
func (e *BatchError) Unwrap() error { return e.Cause }

func validDeskName(name string) bool {
    if name == "" || len([]rune(name)) > maxDeskRunes { return false }
    for _, char := range name {
        if unicode.IsControl(char) { return false }
    }
    return true
}

func validateBatchAction(action BatchAction) (BatchAction, error) {
    action.Desk = strings.TrimSpace(action.Desk)
    action.Source = strings.TrimSpace(action.Source)
    action.Destination = strings.TrimSpace(action.Destination)
    switch action.Op {
    case "issue":
        if action.ID != 0 || action.Desk != "" || action.Source != "" || action.Destination != "" || action.Status != "" {
            return BatchAction{}, ErrInvalidBatchAction
        }
    case "call":
        if action.ID != 0 || !validDeskName(action.Desk) || action.Source != "" || action.Destination != "" || action.Status != "" {
            return BatchAction{}, ErrInvalidBatchAction
        }
    case "resolve":
        if action.ID < 1 || action.Desk != "" || action.Source != "" || action.Destination != "" || (action.Status != "completed" && action.Status != "absent") {
            return BatchAction{}, ErrInvalidBatchAction
        }
    case "cancel", "requeue":
        if action.ID < 1 || action.Desk != "" || action.Source != "" || action.Destination != "" || action.Status != "" {
            return BatchAction{}, ErrInvalidBatchAction
        }
    case "transfer":
        if action.ID < 1 || action.Desk != "" || !validDeskName(action.Source) || !validDeskName(action.Destination) || action.Status != "" || action.Source == action.Destination {
            return BatchAction{}, ErrInvalidBatchAction
        }
    default:
        return BatchAction{}, ErrInvalidBatchAction
    }
    return action, nil
}

func findBatchTicket(tickets []Ticket, id int) int {
    for index := range tickets {
        if tickets[index].ID == id { return index }
    }
    return -1
}

func batchDeskOccupied(tickets []Ticket, desk string) bool {
    for _, ticket := range tickets {
        if ticket.Status == "called" && ticket.Desk == desk { return true }
    }
    return false
}

// applyBatchAction mutates only the private transaction copy. It never touches
// the live queue until all operations have been checked successfully.
func applyBatchAction(tickets *[]Ticket, nextID *int, action BatchAction) (Ticket, error) {
    switch action.Op {
    case "issue":
        if *nextID == int(^uint(0)>>1) { return Ticket{}, ErrBatchIDOverflow }
        issued := Ticket{ID:*nextID, Status:"waiting"}
        *tickets = append(*tickets, issued)
        *nextID++
        return issued, nil
    case "call":
        if batchDeskOccupied(*tickets, action.Desk) { return Ticket{}, ErrDeskBusy }
        for index := range *tickets {
            if (*tickets)[index].Status == "waiting" {
                (*tickets)[index].Status = "called"
                (*tickets)[index].Desk = action.Desk
                return (*tickets)[index], nil
            }
        }
        return Ticket{}, ErrNoWaiting
    case "resolve", "cancel", "requeue", "transfer":
        index := findBatchTicket(*tickets, action.ID)
        if index == -1 { return Ticket{}, ErrTicketMissing }
        ticket := &(*tickets)[index]
        switch action.Op {
        case "resolve":
            if ticket.Status != "called" { return Ticket{}, ErrNotCalled }
            ticket.Status = action.Status
        case "cancel":
            if ticket.Status != "waiting" { return Ticket{}, ErrNotWaiting }
            ticket.Status = "cancelled"
        case "requeue":
            if ticket.Status != "called" { return Ticket{}, ErrNotCalled }
            ticket.Status = "waiting"
            ticket.Desk = ""
        case "transfer":
            if ticket.Status != "called" { return Ticket{}, ErrNotCalled }
            if ticket.Desk != action.Source { return Ticket{}, ErrDeskMismatch }
            if batchDeskOccupied(*tickets, action.Destination) { return Ticket{}, ErrDeskBusy }
            ticket.Desk = action.Destination
        }
        return *ticket, nil
    }
    return Ticket{}, ErrInvalidBatchAction
}

// ApplyBatch executes up to 100 dependent operations atomically. Dry-run uses
// the same validation and state transitions, but leaves the queue unchanged.
// The lock serializes transactions with single-ticket operations and backups.
func (q *Queue) ApplyBatch(actions []BatchAction, dryRun bool) (BatchResult, error) {
    if len(actions) == 0 || len(actions) > maxBatchActions {
        return BatchResult{}, ErrEmptyBatch
    }
    q.mu.Lock()
    defer q.mu.Unlock()
    staged := append([]Ticket(nil), q.items...)
    nextID := q.nextID
    changes := make([]Ticket, 0, len(actions))
    for index, raw := range actions {
        action, err := validateBatchAction(raw)
        if err != nil { return BatchResult{}, &BatchError{Index:index, Cause:err} }
        changed, err := applyBatchAction(&staged, &nextID, action)
        if err != nil { return BatchResult{}, &BatchError{Index:index, Cause:err} }
        changes = append(changes, changed)
    }
    counts := map[string]int{"waiting":0,"called":0,"completed":0,"absent":0,"cancelled":0}
    for _, ticket := range staged { counts[ticket.Status]++ }
    if !dryRun {
        q.items = staged
        q.nextID = nextID
    }
    return BatchResult{DryRun:dryRun, Applied:len(changes), Tickets:changes, NextID:nextID, Counts:counts}, nil
}

func decodeBatchRequest(body io.Reader) (BatchRequest, error) {
    data, err := io.ReadAll(io.LimitReader(body, maxBatchBody+1))
    if err != nil || len(data) > maxBatchBody { return BatchRequest{}, ErrInvalidBatchAction }
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()
    var request BatchRequest
    if err := decoder.Decode(&request); err != nil { return BatchRequest{}, ErrInvalidBatchAction }
    var trailing any
    if err := decoder.Decode(&trailing); err != io.EOF { return BatchRequest{}, ErrInvalidBatchAction }
    if len(request.Actions) == 0 || len(request.Actions) > maxBatchActions {
        return BatchRequest{}, ErrEmptyBatch
    }
    return request, nil
}

func registerBatchRoutes(q *Queue, mux *http.ServeMux) {
    mux.HandleFunc("/tickets/batch", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { badMethod(w); return }
        defer r.Body.Close()
        request, err := decodeBatchRequest(r.Body)
        if err != nil {
            respond(w, http.StatusBadRequest, map[string]string{"erro":err.Error()})
            return
        }
        result, err := q.ApplyBatch(request.Actions, request.DryRun)
        if err != nil {
            status := http.StatusConflict
            if errors.Is(err, ErrInvalidBatchAction) || errors.Is(err, ErrEmptyBatch) || errors.Is(err, ErrBatchIDOverflow) {
                status = http.StatusBadRequest
            }
            respond(w, status, map[string]string{"erro":err.Error()})
            return
        }
        w.Header().Set("Cache-Control", "no-store")
        respond(w, http.StatusOK, result)
    })
}

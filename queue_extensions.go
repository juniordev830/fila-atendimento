package main

import (
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "sort"
    "strconv"
    "strings"
)

var (
    ErrTicketMissing = errors.New("senha inexistente")
    ErrNotWaiting = errors.New("senha nao esta aguardando")
    ErrDeskMismatch = errors.New("senha nao pertence ao guiche informado")
    ErrSameDesk = errors.New("guiches de origem e destino devem ser diferentes")
    ErrInvalidPage = errors.New("paginacao invalida")
    ErrInvalidStatus = errors.New("status de filtro invalido")
)

func validStatus(status string) bool {
    switch status {
    case "waiting", "called", "completed", "absent", "cancelled":
        return true
    }
    return false
}

// Cancel removes only a waiting ticket. IDs remain monotonically increasing.
func (q *Queue) Cancel(id int) (Ticket, error) {
    q.mu.Lock()
    defer q.mu.Unlock()
    for i := range q.items {
        if q.items[i].ID == id {
            if q.items[i].Status != "waiting" {
                return Ticket{}, ErrNotWaiting
            }
            q.items[i].Status = "cancelled"
            return q.items[i], nil
        }
    }
    return Ticket{}, ErrTicketMissing
}

// Requeue returns a called ticket to the waiting queue, freeing its desk.
// Original issuance order is preserved because tickets remain ordered by ID.
func (q *Queue) Requeue(id int) (Ticket, error) {
    q.mu.Lock()
    defer q.mu.Unlock()
    for i := range q.items {
        if q.items[i].ID == id {
            if q.items[i].Status != "called" {
                return Ticket{}, ErrNotCalled
            }
            q.items[i].Status = "waiting"
            q.items[i].Desk = ""
            return q.items[i], nil
        }
    }
    return Ticket{}, ErrTicketMissing
}

// Transfer changes the desk of an active ticket atomically.
func (q *Queue) Transfer(id int, source, destination string) (Ticket, error) {
    source, destination = strings.TrimSpace(source), strings.TrimSpace(destination)
    if source == "" || destination == "" {
        return Ticket{}, errors.New("guiches obrigatorios")
    }
    if source == destination {
        return Ticket{}, ErrSameDesk
    }
    q.mu.Lock()
    defer q.mu.Unlock()
    index := -1
    for i := range q.items {
        ticket := q.items[i]
        if ticket.Status == "called" && ticket.Desk == destination {
            return Ticket{}, ErrDeskBusy
        }
        if ticket.ID == id {
            index = i
        }
    }
    if index < 0 {
        return Ticket{}, ErrTicketMissing
    }
    if q.items[index].Status != "called" {
        return Ticket{}, ErrNotCalled
    }
    if q.items[index].Desk != source {
        return Ticket{}, ErrDeskMismatch
    }
    q.items[index].Desk = destination
    return q.items[index], nil
}

// Position reports a 1-based position among waiting tickets.
func (q *Queue) Position(id int) (int, error) {
    q.mu.Lock()
    defer q.mu.Unlock()
    position := 0
    for _, ticket := range q.items {
        if ticket.Status == "waiting" {
            position++
        }
        if ticket.ID == id {
            if ticket.Status != "waiting" {
                return 0, ErrNotWaiting
            }
            return position, nil
        }
    }
    return 0, ErrTicketMissing
}

func (q *Queue) Stats() map[string]any {
    q.mu.Lock()
    defer q.mu.Unlock()
    counts := map[string]int{
        "waiting": 0, "called": 0, "completed": 0, "absent": 0, "cancelled": 0,
    }
    desks := make([]string, 0)
    for _, ticket := range q.items {
        counts[ticket.Status]++
        if ticket.Status == "called" {
            desks = append(desks, ticket.Desk)
        }
    }
    sort.Strings(desks)
    return map[string]any{
        "total": len(q.items),
        "next_id": q.nextID,
        "counts": counts,
        "active_desks": desks,
    }
}

// Page limits result sizes to prevent unbounded HTTP responses.
func (q *Queue) Page(status string, offset, limit int) (map[string]any, error) {
    if status != "" && !validStatus(status) {
        return nil, ErrInvalidStatus
    }
    if offset < 0 || limit < 1 || limit > 100 {
        return nil, ErrInvalidPage
    }
    q.mu.Lock()
    defer q.mu.Unlock()
    items := make([]Ticket, 0, limit)
    total := 0
    for _, ticket := range q.items {
        if status != "" && ticket.Status != status {
            continue
        }
        if total >= offset && len(items) < limit {
            items = append(items, ticket)
        }
        total++
    }
    return map[string]any{
        "items": items, "total": total, "offset": offset,
        "limit": limit, "status": status,
    }, nil
}

// ExportSnapshot emits a portable, versioned snapshot with the next ID.
func (q *Queue) ExportSnapshot() ([]byte, error) {
    q.mu.Lock()
    defer q.mu.Unlock()
    return json.Marshal(map[string]any{
        "version": 1, "next_id": q.nextID, "tickets": q.items,
    })
}

// RestoreQueue rejects malformed or inconsistent snapshots before exposing state.
func RestoreQueue(data []byte) (*Queue, error) {
    if len(data) > 5*1024*1024 {
        return nil, errors.New("snapshot excede 5 MiB")
    }
    var envelope struct {
        Version int `json:"version"`
        NextID int `json:"next_id"`
        Tickets []Ticket `json:"tickets"`
    }
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()
    if err := decoder.Decode(&envelope); err != nil {
        return nil, fmt.Errorf("snapshot invalido: %w", err)
    }
    var extra any
    if err := decoder.Decode(&extra); err != io.EOF {
        return nil, errors.New("snapshot contem dados extras")
    }
    if envelope.Version != 1 || envelope.NextID < 1 || len(envelope.Tickets) > 100000 {
        return nil, errors.New("metadados de snapshot invalidos")
    }
    activeDesks := make(map[string]bool)
    previousID := 0
    for _, ticket := range envelope.Tickets {
        if ticket.ID <= previousID || ticket.ID >= envelope.NextID || !validStatus(ticket.Status) {
            return nil, errors.New("ordem, identificador ou status de senha invalido")
        }
        if ticket.Status == "called" {
            if strings.TrimSpace(ticket.Desk) == "" || activeDesks[ticket.Desk] {
                return nil, errors.New("guiche ativo invalido ou duplicado")
            }
            activeDesks[ticket.Desk] = true
        } else if ticket.Desk != "" && ticket.Status == "waiting" {
            return nil, errors.New("senha aguardando nao pode ocupar guiche")
        }
        previousID = ticket.ID
    }
    return &Queue{nextID: envelope.NextID, items: append([]Ticket(nil), envelope.Tickets...)}, nil
}

func parsePageQuery(r *http.Request) (string, int, int, error) {
    query := r.URL.Query()
    for key := range query {
        if key != "status" && key != "offset" && key != "limit" {
            return "", 0, 0, ErrInvalidPage
        }
        if len(query[key]) != 1 {
            return "", 0, 0, ErrInvalidPage
        }
    }
    status, offset, limit := query.Get("status"), 0, 20
    if raw := query.Get("offset"); raw != "" {
        parsed, err := strconv.Atoi(raw)
        if err != nil { return "", 0, 0, ErrInvalidPage }
        offset = parsed
    }
    if raw := query.Get("limit"); raw != "" {
        parsed, err := strconv.Atoi(raw)
        if err != nil { return "", 0, 0, ErrInvalidPage }
        limit = parsed
    }
    if offset < 0 || limit < 1 || limit > 100 {
        return "", 0, 0, ErrInvalidPage
    }
    return status, offset, limit, nil
}

func withAdvancedRoutes(q *Queue, mux *http.ServeMux) http.Handler {
    mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { badMethod(w); return }
        respond(w, http.StatusOK, q.Stats())
    })
    mux.HandleFunc("/tickets/search", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { badMethod(w); return }
        status, offset, limit, err := parsePageQuery(r)
        if err != nil {
            respond(w, http.StatusBadRequest, map[string]string{"erro": err.Error()}); return
        }
        page, err := q.Page(status, offset, limit)
        if err != nil {
            respond(w, http.StatusBadRequest, map[string]string{"erro": err.Error()}); return
        }
        respond(w, http.StatusOK, page)
    })
    mux.HandleFunc("/tickets/position", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { badMethod(w); return }
        id, err := strconv.Atoi(r.URL.Query().Get("id"))
        if err != nil || id < 1 {
            respond(w, http.StatusBadRequest, map[string]string{"erro": "id invalido"}); return
        }
        position, err := q.Position(id)
        if err != nil {
            respond(w, http.StatusConflict, map[string]string{"erro": err.Error()}); return
        }
        respond(w, http.StatusOK, map[string]int{"id": id, "position": position})
    })
    mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { badMethod(w); return }
        var input struct { ID int `json:"id"` }
        if err := readObject(r, &input); err != nil || input.ID < 1 {
            respond(w, http.StatusBadRequest, map[string]string{"erro": "id ou JSON invalido"}); return
        }
        ticket, err := q.Cancel(input.ID)
        if err != nil {
            respond(w, http.StatusConflict, map[string]string{"erro": err.Error()}); return
        }
        respond(w, http.StatusOK, ticket)
    })
    mux.HandleFunc("/requeue", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { badMethod(w); return }
        var input struct { ID int `json:"id"` }
        if err := readObject(r, &input); err != nil || input.ID < 1 {
            respond(w, http.StatusBadRequest, map[string]string{"erro": "id ou JSON invalido"}); return
        }
        ticket, err := q.Requeue(input.ID)
        if err != nil {
            respond(w, http.StatusConflict, map[string]string{"erro": err.Error()}); return
        }
        respond(w, http.StatusOK, ticket)
    })
    mux.HandleFunc("/transfer", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { badMethod(w); return }
        var input struct {
            ID int `json:"id"`
            Source string `json:"source"`
            Destination string `json:"destination"`
        }
        if err := readObject(r, &input); err != nil || input.ID < 1 || strings.TrimSpace(input.Source) == "" || strings.TrimSpace(input.Destination) == "" {
            respond(w, http.StatusBadRequest, map[string]string{"erro": "campos ou JSON invalidos"}); return
        }
        ticket, err := q.Transfer(input.ID, input.Source, input.Destination)
        if err != nil {
            respond(w, http.StatusConflict, map[string]string{"erro": err.Error()}); return
        }
        respond(w, http.StatusOK, ticket)
    })
    mux.HandleFunc("/export", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { badMethod(w); return }
        data, err := q.ExportSnapshot()
        if err != nil {
            respond(w, http.StatusInternalServerError, map[string]string{"erro": "erro ao exportar"}); return
        }
        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        w.Header().Set("Cache-Control", "no-store")
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write(data)
    })
    registerReportRoutes(q, mux)
    return mux
}

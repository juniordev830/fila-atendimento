package main

import (
  "errors"
  "html/template"
  "log"
  "net/http"
  "strconv"
  "strings"
  "sync"
)

type Ticket struct { Number int; Status string; Desk string }
type Queue struct { mu sync.Mutex; next int; tickets []Ticket }

func (q *Queue) Issue() Ticket {
 q.mu.Lock(); defer q.mu.Unlock()
 q.next++
 t := Ticket{Number:q.next, Status:"aguardando"}
 q.tickets = append(q.tickets,t)
 return t
}
func (q *Queue) Call(desk string) (Ticket,error) {
 desk=strings.TrimSpace(desk)
 if desk=="" {return Ticket{}, errors.New("informe o guichê")}
 q.mu.Lock(); defer q.mu.Unlock()
 for _,t:=range q.tickets {if t.Desk==desk && t.Status=="chamada" {return Ticket{},errors.New("guichê já possui atendimento ativo")}}
 for i:=range q.tickets {
  if q.tickets[i].Status=="aguardando" {
   q.tickets[i].Status="chamada"; q.tickets[i].Desk=desk
   return q.tickets[i],nil
  }
 }
 return Ticket{}, errors.New("fila vazia")
}
func (q *Queue) Close(number int,desk,status string) error {
 if status!="atendida" && status!="ausente" {return errors.New("situação inválida")}
 q.mu.Lock();defer q.mu.Unlock()
 for i:=range q.tickets {
  t:=&q.tickets[i]
  if t.Number==number {
   if t.Status!="chamada" || t.Desk!=strings.TrimSpace(desk) {return errors.New("senha não está ativa nesse guichê")}
   t.Status=status;return nil
  }
 }
 return errors.New("senha não encontrada")
}
func (q *Queue) List() []Ticket {
 q.mu.Lock();defer q.mu.Unlock()
 return append([]Ticket(nil),q.tickets...)
}
var page=template.Must(template.New("page").Parse(`<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Fila de atendimento</title><style>body{font:16px system-ui;max-width:780px;margin:3rem auto;padding:0 1rem;background:#f5f7fb;color:#17243d}main{background:white;padding:2rem;border-radius:16px}button,input,select{padding:.65rem;border-radius:7px;border:1px solid #bbc6db}button{background:#175dcc;color:white;cursor:pointer}form{display:inline-block;margin:.4rem}table{width:100%;border-collapse:collapse}td,th{padding:.7rem;border-bottom:1px solid #ddd;text-align:left}label{display:inline-block;margin:.3rem}</style></head><body><main><h1>Fila de atendimento</h1><p>Senhas em ordem de chegada. Dados apenas em memória.</p><form method="post" action="/emitir"><button>Emitir senha</button></form><form method="post" action="/chamar"><label>Guichê <input required name="guiche" value="1" maxlength="20"></label><button>Chamar próxima</button></form><h2>Senhas</h2><table><thead><tr><th>Senha</th><th>Estado</th><th>Guichê</th><th>Ação</th></tr></thead><tbody>{{range .}}<tr><td>{{.Number}}</td><td>{{.Status}}</td><td>{{.Desk}}</td><td>{{if eq .Status "chamada"}}<form method="post" action="/encerrar"><input type="hidden" name="numero" value="{{.Number}}"><input type="hidden" name="guiche" value="{{.Desk}}"><select name="status"><option value="atendida">Atendida</option><option value="ausente">Ausente</option></select><button>Encerrar</button></form>{{end}}</td></tr>{{else}}<tr><td colspan="4">Nenhuma senha emitida.</td></tr>{{end}}</tbody></table></main></body></html>`))

func app(q *Queue) http.Handler {
 mux:=http.NewServeMux()
 mux.HandleFunc("GET /",func(w http.ResponseWriter,r *http.Request){if err:=page.Execute(w,q.List());err!=nil{log.Println(err)}})
 mux.HandleFunc("POST /emitir",func(w http.ResponseWriter,r *http.Request){q.Issue();http.Redirect(w,r,"/",http.StatusSeeOther)})
 mux.HandleFunc("POST /chamar",func(w http.ResponseWriter,r *http.Request){if _,err:=q.Call(r.FormValue("guiche"));err!=nil{http.Error(w,err.Error(),http.StatusConflict);return};http.Redirect(w,r,"/",http.StatusSeeOther)})
 mux.HandleFunc("POST /encerrar",func(w http.ResponseWriter,r *http.Request){i,err:=strconv.Atoi(r.FormValue("numero"));if err!=nil{http.Error(w,"senha inválida",http.StatusBadRequest);return};if err:=q.Close(i,r.FormValue("guiche"),r.FormValue("status"));err!=nil{http.Error(w,err.Error(),http.StatusConflict);return};http.Redirect(w,r,"/",http.StatusSeeOther)})
 return mux
}
func main(){log.Println("Acesse http://127.0.0.1:8080");log.Fatal(http.ListenAndServe("127.0.0.1:8080",app(&Queue{})))}

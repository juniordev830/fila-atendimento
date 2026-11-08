package main

import (
 "encoding/csv"
 "errors"
 "net/http"
 "sort"
 "strconv"
 "strings"
)

var (
 ErrReportRange = errors.New("intervalo de senhas invalido")
 ErrReportLimit = errors.New("limite de relatorio excedido")
)

// QueueReport summarizes the current state of tickets, not historical events.
type QueueReport struct {
 FromID int `json:"from_id"`
 ToID int `json:"to_id"`
 Total int `json:"total"`
 Counts map[string]int `json:"counts"`
 ActiveDesks []string `json:"active_desks"`
 Finished int `json:"finished"`
 CompletionPercent float64 `json:"completion_percent"`
}

type DeskReport struct {
 Desk string `json:"desk"`
 Called int `json:"called"`
 Completed int `json:"completed"`
 Absent int `json:"absent"`
 Total int `json:"total"`
}

type DeskReportPage struct {
 FromID int `json:"from_id"`
 ToID int `json:"to_id"`
 Total int `json:"total"`
 Offset int `json:"offset"`
 Limit int `json:"limit"`
 Items []DeskReport `json:"items"`
}

func validateReportRange(fromID,toID int) error {
 if fromID < 1 || toID < fromID { return ErrReportRange }
 return nil
}

// reportTickets copies under the mutex, then releases it before expensive work.
// A hard cap protects the HTTP process against unbounded report requests.
func (q *Queue) reportTickets(fromID,toID int)([]Ticket,error){
 if err:=validateReportRange(fromID,toID);err!=nil{return nil,err}
 q.mu.Lock()
 defer q.mu.Unlock()
 selected:=make([]Ticket,0)
 for _,ticket:=range q.items{
  if ticket.ID<fromID{continue}
  if ticket.ID>toID{break}
  if len(selected)==10000{return nil,ErrReportLimit}
  selected=append(selected,ticket)
 }
 return selected,nil
}

// CompletionPercent is completed/(completed+absent), excluding pending tickets.
func (q *Queue) Report(fromID,toID int)(QueueReport,error){
 tickets,err:=q.reportTickets(fromID,toID)
 if err!=nil{return QueueReport{},err}
 counts:=map[string]int{"waiting":0,"called":0,"completed":0,"absent":0,"cancelled":0}
 active:=make([]string,0)
 for _,ticket:=range tickets{
  counts[ticket.Status]++
  if ticket.Status=="called"{active=append(active,ticket.Desk)}
 }
 sort.Strings(active)
 finished:=counts["completed"]+counts["absent"]
 percent:=0.0
 if finished>0{percent=float64(counts["completed"])*100/float64(finished)}
 return QueueReport{FromID:fromID,ToID:toID,Total:len(tickets),Counts:counts,ActiveDesks:active,Finished:finished,CompletionPercent:percent},nil
}

// Desks groups the last known service desk by status, with stable pagination.
func (q *Queue) Desks(fromID,toID,offset,limit int)(DeskReportPage,error){
 if offset<0||limit<1||limit>100{return DeskReportPage{},ErrReportLimit}
 tickets,err:=q.reportTickets(fromID,toID)
 if err!=nil{return DeskReportPage{},err}
 byDesk:=make(map[string]*DeskReport)
 for _,ticket:=range tickets{
  if ticket.Desk==""||(ticket.Status!="called"&&ticket.Status!="completed"&&ticket.Status!="absent"){continue}
  row:=byDesk[ticket.Desk]
  if row==nil{row=&DeskReport{Desk:ticket.Desk};byDesk[ticket.Desk]=row}
  switch ticket.Status{
  case "called":row.Called++
  case "completed":row.Completed++
  case "absent":row.Absent++
  }
  row.Total++
 }
 names:=make([]string,0,len(byDesk))
 for name:=range byDesk{names=append(names,name)}
 sort.Strings(names)
 page:=DeskReportPage{FromID:fromID,ToID:toID,Total:len(names),Offset:offset,Limit:limit,Items:[]DeskReport{}}
 for i:=offset;i<len(names)&&len(page.Items)<limit;i++{page.Items=append(page.Items,*byDesk[names[i]])}
 return page,nil
}

// Prefix formula-like desk values to prevent spreadsheet CSV injection.
func safeCSVCell(value string)string{
 trimmed:=strings.TrimLeft(value," \t\r\n")
 if trimmed!=""&&strings.ContainsRune("=+-@",rune(trimmed[0])){return "'"+value}
 return value
}

func(q *Queue)ExportReportCSV(fromID,toID int)(string,error){
 tickets,err:=q.reportTickets(fromID,toID)
 if err!=nil{return "",err}
 var b strings.Builder
 writer:=csv.NewWriter(&b)
 if err:=writer.Write([]string{"id","status","desk"});err!=nil{return "",err}
 for _,ticket:=range tickets{
  if err:=writer.Write([]string{strconv.Itoa(ticket.ID),ticket.Status,safeCSVCell(ticket.Desk)});err!=nil{return "",err}
 }
 writer.Flush()
 if err:=writer.Error();err!=nil{return "",err}
 return b.String(),nil
}

func reportQuery(r *http.Request,allowed ...string)(map[string]int,error){
 permitted:=map[string]bool{"from_id":true,"to_id":true}
 values:=map[string]int{"from_id":1,"to_id":int(^uint(0)>>1)}
 for _,key:=range allowed{permitted[key]=true}
 for key,entries:=range r.URL.Query(){
  if !permitted[key]||len(entries)!=1||entries[0]==""{return nil,ErrReportRange}
  value,err:=strconv.Atoi(entries[0])
  if err!=nil||value<0{return nil,ErrReportRange}
  values[key]=value
 }
 if err:=validateReportRange(values["from_id"],values["to_id"]);err!=nil{return nil,err}
 return values,nil
}

func registerReportRoutes(q *Queue,mux *http.ServeMux){
 mux.HandleFunc("/reports/summary",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{badMethod(w);return}
  params,err:=reportQuery(r)
  if err!=nil{respond(w,http.StatusBadRequest,map[string]string{"erro":err.Error()});return}
  report,err:=q.Report(params["from_id"],params["to_id"])
  if err!=nil{respond(w,http.StatusUnprocessableEntity,map[string]string{"erro":err.Error()});return}
  w.Header().Set("Cache-Control","no-store")
  respond(w,http.StatusOK,report)
 })
 mux.HandleFunc("/reports/desks",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{badMethod(w);return}
  params,err:=reportQuery(r,"offset","limit")
  if err!=nil{respond(w,http.StatusBadRequest,map[string]string{"erro":err.Error()});return}
  offset,limit:=params["offset"],params["limit"]
  if _,exists:=r.URL.Query()["limit"];!exists{limit=20}
  report,err:=q.Desks(params["from_id"],params["to_id"],offset,limit)
  if err!=nil{respond(w,http.StatusBadRequest,map[string]string{"erro":err.Error()});return}
  w.Header().Set("Cache-Control","no-store")
  respond(w,http.StatusOK,report)
 })
 mux.HandleFunc("/reports/export.csv",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{badMethod(w);return}
  params,err:=reportQuery(r)
  if err!=nil{respond(w,http.StatusBadRequest,map[string]string{"erro":err.Error()});return}
  content,err:=q.ExportReportCSV(params["from_id"],params["to_id"])
  if err!=nil{respond(w,http.StatusUnprocessableEntity,map[string]string{"erro":err.Error()});return}
  w.Header().Set("Content-Type","text/csv; charset=utf-8")
  w.Header().Set("Content-Disposition","attachment; filename=\"fila-relatorio.csv\"")
  w.Header().Set("Cache-Control","no-store")
  w.WriteHeader(http.StatusOK)
  _,_=w.Write([]byte(content))
 })
}

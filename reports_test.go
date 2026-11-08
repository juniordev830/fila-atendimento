package main

import (
 "encoding/csv"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync"
 "testing"
)

func makeReportQueue(t *testing.T)*Queue{
 t.Helper()
 q:=NewQueue()
 for i:=0;i<6;i++{q.Issue()}
 if _,err:=q.Call("A");err!=nil{t.Fatal(err)}
 if _,err:=q.Resolve(1,"completed");err!=nil{t.Fatal(err)}
 if _,err:=q.Call("B");err!=nil{t.Fatal(err)}
 if _,err:=q.Resolve(2,"absent");err!=nil{t.Fatal(err)}
 if _,err:=q.Cancel(3);err!=nil{t.Fatal(err)}
 if _,err:=q.Call("A");err!=nil{t.Fatal(err)}
 return q
}

func TestReportSummaryCountsAndRanges(t *testing.T){
 q:=makeReportQueue(t)
 report,err:=q.Report(1,6)
 if err!=nil{t.Fatal(err)}
 if report.Total!=6||report.Counts["completed"]!=1||report.Counts["absent"]!=1||report.Counts["cancelled"]!=1||report.Counts["called"]!=1||report.Counts["waiting"]!=2{t.Fatalf("summary: %+v",report)}
 if report.Finished!=2||report.CompletionPercent!=50||len(report.ActiveDesks)!=1||report.ActiveDesks[0]!="A"{t.Fatalf("completion: %+v",report)}
 subset,err:=q.Report(3,5)
 if err!=nil||subset.Total!=3||subset.Counts["completed"]!=0||subset.CompletionPercent!=0{t.Fatalf("subset: %+v %v",subset,err)}
 empty,err:=q.Report(99,100)
 if err!=nil||empty.Total!=0||empty.Finished!=0||empty.CompletionPercent!=0{t.Fatalf("empty: %+v %v",empty,err)}
 for _,tc:=range [][2]int{{0,1},{5,4},{-1,5}}{
  if _,err:=q.Report(tc[0],tc[1]);!errors.Is(err,ErrReportRange){t.Fatalf("range %v: %v",tc,err)}
 }
}

func TestDeskReportSortedAndPaged(t *testing.T){
 q:=makeReportQueue(t)
 report,err:=q.Desks(1,6,0,10)
 if err!=nil||report.Total!=2||len(report.Items)!=2{t.Fatalf("report: %+v %v",report,err)}
 if report.Items[0].Desk!="A"||report.Items[0].Completed!=1||report.Items[0].Called!=1||report.Items[0].Total!=2{t.Fatalf("desk A: %+v",report.Items[0])}
 if report.Items[1].Desk!="B"||report.Items[1].Absent!=1{t.Fatalf("desk B: %+v",report.Items[1])}
 second,err:=q.Desks(1,6,1,1)
 if err!=nil||len(second.Items)!=1||second.Items[0].Desk!="B"{t.Fatalf("second: %+v %v",second,err)}
 empty,err:=q.Desks(1,6,99,10)
 if err!=nil||len(empty.Items)!=0||empty.Total!=2{t.Fatalf("empty: %+v %v",empty,err)}
 filtered,err:=q.Desks(1,1,0,10)
 if err!=nil||filtered.Total!=1||filtered.Items[0].Completed!=1{t.Fatalf("filtered: %+v %v",filtered,err)}
 for _,tc:=range [][2]int{{-1,10},{0,0},{0,101}}{
  if _,err:=q.Desks(1,6,tc[0],tc[1]);!errors.Is(err,ErrReportLimit){t.Fatalf("page %v: %v",tc,err)}
 }
}

func TestCSVExportEscapesFormulaAndPreservesRows(t *testing.T){
 q:=NewQueue();q.Issue();q.Issue()
 if _,err:=q.Call(" =HYPERLINK(\"evil\")");err!=nil{t.Fatal(err)}
 content,err:=q.ExportReportCSV(1,2)
 if err!=nil{t.Fatal(err)}
 rows,err:=csv.NewReader(strings.NewReader(content)).ReadAll()
 if err!=nil{t.Fatal(err)}
 if len(rows)!=3||rows[0][0]!="id"||rows[1][0]!="1"||rows[1][1]!="called"||!strings.HasPrefix(rows[1][2],"'")||rows[2][1]!="waiting"{t.Fatalf("rows: %#v",rows)}
 if _,err:=q.ExportReportCSV(2,1);!errors.Is(err,ErrReportRange){t.Fatalf("range: %v",err)}
 for _,v:=range []string{"=1+1","+SUM(A1)","-1","@foo"," \t=cmd"}{
  if !strings.HasPrefix(safeCSVCell(v),"'"){t.Fatalf("formula: %q",v)}
 }
 for _,v:=range []string{"A","1",""}{
  if safeCSVCell(v)!=v{t.Fatalf("unexpected escape: %q",v)}
 }
}

func TestReportHTTPRoutesAndAuth(t *testing.T){
 q:=makeReportQueue(t)
 h:=secureHandler(q,"secret")
 cases:=[]struct{method,path,auth string;status int}{
  {"GET","/reports/summary","",http.StatusUnauthorized},
  {"GET","/reports/desks","",http.StatusUnauthorized},
  {"GET","/reports/export.csv","",http.StatusUnauthorized},
  {"GET","/reports/summary","Bearer secret",http.StatusOK},
  {"GET","/reports/summary?from_id=2&to_id=4","Bearer secret",http.StatusOK},
  {"GET","/reports/summary?from_id=7&to_id=2","Bearer secret",http.StatusBadRequest},
  {"GET","/reports/summary?from_id=0","Bearer secret",http.StatusBadRequest},
  {"GET","/reports/summary?unexpected=1","Bearer secret",http.StatusBadRequest},
  {"GET","/reports/summary?from_id=1&from_id=2","Bearer secret",http.StatusBadRequest},
  {"GET","/reports/summary?to_id=abc","Bearer secret",http.StatusBadRequest},
  {"GET","/reports/desks?offset=1&limit=1","Bearer secret",http.StatusOK},
  {"GET","/reports/desks?offset=-1","Bearer secret",http.StatusBadRequest},
  {"GET","/reports/desks?limit=101","Bearer secret",http.StatusBadRequest},
  {"GET","/reports/export.csv","Bearer secret",http.StatusOK},
  {"GET","/reports/export.csv?limit=5","Bearer secret",http.StatusBadRequest},
  {"POST","/reports/summary","Bearer secret",http.StatusMethodNotAllowed},
  {"POST","/reports/desks","Bearer secret",http.StatusMethodNotAllowed},
  {"POST","/reports/export.csv","Bearer secret",http.StatusMethodNotAllowed},
 }
 for _,tc:=range cases{
  t.Run(tc.method+tc.path+tc.auth,func(t *testing.T){
   r:=httptest.NewRequest(tc.method,tc.path,nil)
   if tc.auth!=""{r.Header.Set("Authorization",tc.auth)}
   w:=httptest.NewRecorder();h.ServeHTTP(w,r)
   if w.Code!=tc.status{t.Fatalf("status %d != %d: %s",w.Code,tc.status,w.Body.String())}
   if tc.status==http.StatusOK&&w.Header().Get("Cache-Control")!="no-store"{t.Fatal("no-store missing")}
   if tc.status==http.StatusOK&&strings.Contains(tc.path,"export.csv"){
    if w.Header().Get("Content-Type")!="text/csv; charset=utf-8"||!strings.HasPrefix(w.Body.String(),"id,status,desk"){t.Fatalf("CSV: %s",w.Body.String())}
   }
   if tc.status==http.StatusOK&&strings.Contains(tc.path,"summary"){
    var report QueueReport
    if err:=json.Unmarshal(w.Body.Bytes(),&report);err!=nil{t.Fatal(err)}
    if report.Total==0{t.Fatal("empty report")}
   }
  })
 }
}

func TestConcurrentReportsHaveConsistentCounts(t *testing.T){
 q:=NewQueue()
 var wg sync.WaitGroup
 for i:=0;i<100;i++{
  wg.Add(1);go func(){defer wg.Done();q.Issue()}()
 }
 for i:=0;i<20;i++{
  wg.Add(1);go func(){
   defer wg.Done()
   for j:=0;j<10;j++{
    report,err:=q.Report(1,1000)
    if err!=nil{t.Errorf("report: %v",err);return}
    sum:=0
    for _,count:=range report.Counts{sum+=count}
    if sum!=report.Total{t.Errorf("inconsistent snapshot: %d != %d",sum,report.Total);return}
   }
  }()
 }
 wg.Wait()
 report,err:=q.Report(1,1000)
 if err!=nil||report.Total!=100{t.Fatalf("final: %+v %v",report,err)}
}

func TestReportLimitRejectsOversizedExports(t *testing.T){
 q:=NewQueue()
 for i:=0;i<10001;i++{q.Issue()}
 if _,err:=q.Report(1,20000);!errors.Is(err,ErrReportLimit){t.Fatalf("summary limit: %v",err)}
 if _,err:=q.ExportReportCSV(1,20000);!errors.Is(err,ErrReportLimit){t.Fatalf("CSV limit: %v",err)}
 report,err:=q.Report(1,10000)
 if err!=nil||report.Total!=10000{t.Fatalf("bounded: %d %v",report.Total,err)}
}

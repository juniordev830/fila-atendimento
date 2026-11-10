package main

import (
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "net/http/httptest"
    "reflect"
    "strings"
    "sync"
    "testing"
)

func TestBatchDependentOperationsCommitAtomically(t *testing.T) {
    q := NewQueue()
    q.Issue()
    q.Issue()
    actions := []BatchAction{
        {Op:"call", Desk:" A "},
        {Op:"transfer", ID:1, Source:"A", Destination:"B"},
        {Op:"resolve", ID:1, Status:"completed"},
        {Op:"call", Desk:"A"},
        {Op:"requeue", ID:2},
        {Op:"cancel", ID:2},
        {Op:"issue"},
        {Op:"call", Desk:"C"},
    }
    result, err := q.ApplyBatch(actions, false)
    if err != nil { t.Fatal(err) }
    if result.Applied != len(actions) || result.DryRun || result.NextID != 4 {
        t.Fatalf("batch metadata: %+v", result)
    }
    want := []Ticket{
        {ID:1, Status:"completed", Desk:"B"},
        {ID:2, Status:"cancelled"},
        {ID:3, Status:"called", Desk:"C"},
    }
    if got := q.Snapshot(); !reflect.DeepEqual(got,want) {
        t.Fatalf("got %+v want %+v",got,want)
    }
    if result.Tickets[0].Desk != "A" || result.Tickets[1].Desk != "B" || result.Tickets[2].Status != "completed" {
        t.Fatalf("step results: %+v",result.Tickets)
    }
    if result.Counts["completed"] != 1 || result.Counts["cancelled"] != 1 || result.Counts["called"] != 1 {
        t.Fatalf("counts: %+v",result.Counts)
    }
}

func TestBatchRollbackOnLaterConflictPreservesIDs(t *testing.T) {
    q := NewQueue()
    before := q.Snapshot()
    actions := []BatchAction{{Op:"issue"},{Op:"call",Desk:"A"},{Op:"call",Desk:"A"}}
    _, err := q.ApplyBatch(actions,false)
    if !errors.Is(err,ErrDeskBusy) { t.Fatalf("conflict: %v",err) }
    var batchErr *BatchError
    if !errors.As(err,&batchErr) || batchErr.Index != 2 { t.Fatalf("error index: %v",err) }
    if got:=q.Snapshot(); !reflect.DeepEqual(got,before) { t.Fatalf("partial write: %+v",got) }
    if next:=q.Issue(); next.ID!=1 { t.Fatalf("ID consumed by rollback: %+v",next) }
}

func TestBatchDryRunHasSameResultButNoMutation(t *testing.T) {
    q:=NewQueue()
    q.Issue()
    actions:=[]BatchAction{{Op:"issue"},{Op:"call",Desk:"X"},{Op:"resolve",ID:1,Status:"absent"}}
    before:=q.Snapshot()
    preview,err:=q.ApplyBatch(actions,true)
    if err!=nil{t.Fatal(err)}
    if !preview.DryRun || preview.NextID!=3 || preview.Counts["absent"]!=1 {t.Fatalf("preview: %+v",preview)}
    if !reflect.DeepEqual(q.Snapshot(),before){t.Fatal("dry run modified live queue")}
    committed,err:=q.ApplyBatch(actions,false)
    if err!=nil{t.Fatal(err)}
    if !reflect.DeepEqual(preview.Tickets,committed.Tickets) || !reflect.DeepEqual(preview.Counts,committed.Counts){t.Fatal("preview and commit differ")}
}

func TestBatchRejectsMalformedOperations(t *testing.T) {
    cases:=[]struct{name string;action BatchAction}{
        {"unknown",BatchAction{Op:"reset"}},
        {"issue-extra",BatchAction{Op:"issue",ID:1}},
        {"issue-desk",BatchAction{Op:"issue",Desk:"A"}},
        {"call-missing",BatchAction{Op:"call"}},
        {"call-extra",BatchAction{Op:"call",Desk:"A",ID:1}},
        {"resolve-zero",BatchAction{Op:"resolve",Status:"completed"}},
        {"resolve-status",BatchAction{Op:"resolve",ID:1,Status:"cancelled"}},
        {"cancel-extra",BatchAction{Op:"cancel",ID:1,Status:"absent"}},
        {"requeue-zero",BatchAction{Op:"requeue"}},
        {"transfer-empty",BatchAction{Op:"transfer",ID:1,Source:"A"}},
        {"transfer-same",BatchAction{Op:"transfer",ID:1,Source:"A",Destination:" A "}},
        {"transfer-extra",BatchAction{Op:"transfer",ID:1,Source:"A",Destination:"B",Desk:"X"}},
        {"desk-control",BatchAction{Op:"call",Desk:"A\nB"}},
        {"desk-too-long",BatchAction{Op:"call",Desk:strings.Repeat("á",65)}},
    }
    for _,tc:=range cases{
        t.Run(tc.name,func(t *testing.T){
            q:=NewQueue();q.Issue()
            _,err:=q.ApplyBatch([]BatchAction{tc.action},false)
            if !errors.Is(err,ErrInvalidBatchAction){t.Fatalf("accepted %+v: %v",tc.action,err)}
            if q.Snapshot()[0].Status!="waiting"{t.Fatal("failed action modified queue")}
        })
    }
}

func TestBatchValidationLimits(t *testing.T){
    q:=NewQueue()
    for _,size:=range []int{0,101,1000}{
        _,err:=q.ApplyBatch(make([]BatchAction,size),false)
        if !errors.Is(err,ErrEmptyBatch){t.Fatalf("size %d: %v",size,err)}
    }
    actions:=make([]BatchAction,100)
    for i:=range actions{actions[i]=BatchAction{Op:"issue"}}
    result,err:=q.ApplyBatch(actions,false)
    if err!=nil || result.Applied!=100 || q.Issue().ID!=101{t.Fatalf("max valid batch: %+v %v",result,err)}
}

func TestBatchIDOverflowDoesNotConsumeAnyIDs(t *testing.T){
    q:=NewQueue()
    q.nextID=int(^uint(0)>>1)
    _,err:=q.ApplyBatch([]BatchAction{{Op:"issue"}},false)
    if !errors.Is(err,ErrBatchIDOverflow){t.Fatalf("overflow: %v",err)}
    if q.nextID!=int(^uint(0)>>1)||len(q.items)!=0{t.Fatal("overflow mutated queue")}
}

func TestBatchStateTransitionErrors(t *testing.T){
    cases:=[]struct{name string;actions []BatchAction;expected error}{
        {"call-empty",[]BatchAction{{Op:"call",Desk:"A"}},ErrNoWaiting},
        {"cancel-missing",[]BatchAction{{Op:"cancel",ID:123}},ErrTicketMissing},
        {"requeue-waiting",[]BatchAction{{Op:"issue"},{Op:"requeue",ID:1}},ErrNotCalled},
        {"resolve-waiting",[]BatchAction{{Op:"issue"},{Op:"resolve",ID:1,Status:"absent"}},ErrNotCalled},
        {"cancel-called",[]BatchAction{{Op:"issue"},{Op:"call",Desk:"A"},{Op:"cancel",ID:1}},ErrNotWaiting},
        {"transfer-missing",[]BatchAction{{Op:"transfer",ID:10,Source:"A",Destination:"B"}},ErrTicketMissing},
        {"transfer-waiting",[]BatchAction{{Op:"issue"},{Op:"transfer",ID:1,Source:"A",Destination:"B"}},ErrNotCalled},
        {"transfer-source",[]BatchAction{{Op:"issue"},{Op:"call",Desk:"A"},{Op:"transfer",ID:1,Source:"X",Destination:"B"}},ErrDeskMismatch},
        {"transfer-busy",[]BatchAction{{Op:"issue"},{Op:"issue"},{Op:"call",Desk:"A"},{Op:"call",Desk:"B"},{Op:"transfer",ID:1,Source:"A",Destination:"B"}},ErrDeskBusy},
    }
    for _,tc:=range cases{
        t.Run(tc.name,func(t *testing.T){
            q:=NewQueue()
            _,err:=q.ApplyBatch(tc.actions,false)
            if !errors.Is(err,tc.expected){t.Fatalf("got %v expected %v",err,tc.expected)}
            if q.nextID!=1 || len(q.Snapshot())!=0{t.Fatal("partial transaction committed")}
        })
    }
}

func TestBatchRepeatedIDsAndDeskRelease(t *testing.T){
    q:=NewQueue()
    actions:=[]BatchAction{
        {Op:"issue"},{Op:"issue"},{Op:"call",Desk:"A"},
        {Op:"resolve",ID:1,Status:"completed"},{Op:"call",Desk:"A"},
        {Op:"requeue",ID:2},{Op:"call",Desk:"A"},
    }
    result,err:=q.ApplyBatch(actions,false)
    if err!=nil{t.Fatal(err)}
    if result.Tickets[len(result.Tickets)-1].ID!=2{t.Fatalf("desk not reused: %+v",result)}
    if len(q.Snapshot())!=2{t.Fatal("unexpected ticket duplication")}
}

func TestBatchHTTPValidationAndStatuses(t *testing.T){
    q:=NewQueue();mux:=http.NewServeMux();registerBatchRoutes(q,mux)
    cases:=[]struct{method,body string;status int}{
        {"GET","",http.StatusMethodNotAllowed},
        {"POST","",http.StatusBadRequest},
        {"POST","null",http.StatusBadRequest},
        {"POST","{}",http.StatusBadRequest},
        {"POST",`{"actions":[]}`,http.StatusBadRequest},
        {"POST",`{"actions":[{"op":"issue"}],"unknown":true}`,http.StatusBadRequest},
        {"POST",`{"actions":[{"op":"issue","extra":1}]}`,http.StatusBadRequest},
        {"POST",`{"actions":[{"op":"issue"}]} {}`,http.StatusBadRequest},
        {"POST",`{"actions":[{"op":"call","desk":"A"}]}`,http.StatusConflict},
        {"POST",`{"actions":[{"op":"call"}]}`,http.StatusBadRequest},
        {"POST",`{"actions":[{"op":"issue"}],"dry_run":true}`,http.StatusOK},
        {"POST",`{"actions":[{"op":"issue"}]}`,http.StatusOK},
    }
    for i,tc:=range cases{
        t.Run(fmt.Sprintf("case-%d",i),func(t *testing.T){
            w:=httptest.NewRecorder()
            mux.ServeHTTP(w,httptest.NewRequest(tc.method,"/tickets/batch",strings.NewReader(tc.body)))
            if w.Code!=tc.status{t.Fatalf("status %d expected %d: %s",w.Code,tc.status,w.Body.String())}
            if tc.status==http.StatusOK && w.Header().Get("Cache-Control")!="no-store"{t.Fatal("missing no-store")}
            if !strings.Contains(w.Header().Get("Content-Type"),"application/json"){t.Fatal("missing json response")}
        })
    }
    if len(q.Snapshot())!=1{t.Fatal("HTTP validation or preview changed queue")}
}

func TestBatchHTTPReportsFirstErrorAndRollback(t *testing.T){
    q:=NewQueue();mux:=http.NewServeMux();registerBatchRoutes(q,mux)
    body:=`{"actions":[{"op":"issue"},{"op":"call","desk":"A"},{"op":"call","desk":"A"}]}`
    w:=httptest.NewRecorder()
    mux.ServeHTTP(w,httptest.NewRequest("POST","/tickets/batch",strings.NewReader(body)))
    if w.Code!=http.StatusConflict || !strings.Contains(w.Body.String(),"operacao 3"){
        t.Fatalf("error: %d %s",w.Code,w.Body.String())
    }
    if q.Issue().ID!=1{t.Fatal("HTTP error consumed an ID")}
}

func TestBatchBodyBoundAndUnknownJSON(t *testing.T){
    for _,body:=range []string{
        strings.Repeat(" ",maxBatchBody+1),
        `{"actions":null}`,
        `{"actions":[null]}`,
        `{"actions":[{"op":"issue"}],"dry_run":"yes"}`,
        `{"actions":[{"op":"issue"}],"dry_run":false} trailing`,
    }{
        request,err:=decodeBatchRequest(strings.NewReader(body))
        if err==nil && len(request.Actions)==0{t.Fatalf("accepted empty actions: %q",body[:min(30,len(body))])}
    }
}

func TestBatchConcurrentTransactionsDoNotLoseIDs(t *testing.T){
    q:=NewQueue()
    var wg sync.WaitGroup
    errorsFound:=make(chan error,50)
    for i:=0;i<50;i++{
        wg.Add(1)
        go func(){
            defer wg.Done()
            _,err:=q.ApplyBatch([]BatchAction{{Op:"issue"},{Op:"issue"}},false)
            if err!=nil{errorsFound<-err}
        }()
    }
    wg.Wait();close(errorsFound)
    for err:=range errorsFound{t.Error(err)}
    tickets:=q.Snapshot()
    if len(tickets)!=100{t.Fatalf("lost tickets: %d",len(tickets))}
    for i,ticket:=range tickets{
        if ticket.ID!=i+1 || ticket.Status!="waiting"{t.Fatalf("ticket %d: %+v",i,ticket)}
    }
}

func TestBatchConcurrentPreviewDoesNotMutateQueue(t *testing.T){
    q:=NewQueue()
    for i:=0;i<20;i++{q.Issue()}
    var wg sync.WaitGroup
    for i:=0;i<30;i++{
        wg.Add(1)
        go func(){
            defer wg.Done()
            _,err:=q.ApplyBatch([]BatchAction{{Op:"call",Desk:"A"},{Op:"resolve",ID:1,Status:"completed"}},true)
            if err!=nil{t.Error(err)}
        }()
    }
    wg.Wait()
    if q.Snapshot()[0].Status!="waiting" || q.nextID!=21{t.Fatal("preview leaked state")}
}

func TestBatchResultJSONContract(t *testing.T){
    q:=NewQueue()
    result,err:=q.ApplyBatch([]BatchAction{{Op:"issue"}},true)
    if err!=nil{t.Fatal(err)}
    data,err:=json.Marshal(result)
    if err!=nil{t.Fatal(err)}
    var output map[string]any
    if err:=json.Unmarshal(data,&output);err!=nil{t.Fatal(err)}
    for _,key:=range []string{"dry_run","applied","tickets","next_id","counts"}{
        if _,ok:=output[key];!ok{t.Fatalf("missing field %s: %s",key,data)}
    }
    if bytes.Contains(data,[]byte(`"desk":""`)){t.Fatal("empty desk should be omitted")}
}

package main
import ("testing";"net/http";"net/http/httptest";"strings")
func TestFIFOAndDeskExclusivity(t *testing.T){
 q:=&Queue{};a:=q.Issue();b:=q.Issue()
 if a.Number!=1||b.Number!=2 {t.Fatal("senhas fora da sequência")}
 x,e:=q.Call("1");if e!=nil||x.Number!=1 {t.Fatal(x,e)}
 if _,e:=q.Call("1");e==nil {t.Fatal("guichê ocupado aceitou outra senha")}
 y,e:=q.Call("2");if e!=nil||y.Number!=2 {t.Fatal(y,e)}
 if _,e:=q.Call("3");e==nil {t.Fatal("fila vazia")}
 if e:=q.Close(1,"2","atendida");e==nil {t.Fatal("guichê incorreto")}
 if e:=q.Close(1,"1","atendida");e!=nil {t.Fatal(e)}
 if e:=q.Close(1,"1","ausente");e==nil {t.Fatal("atendimento encerrado duas vezes")}
 if e:=q.Close(2,"2","ausente");e!=nil {t.Fatal(e)}
}
func TestHTTPFlow(t *testing.T){
 h:=app(&Queue{})
 req:=httptest.NewRequest(http.MethodPost,"/emitir",nil);w:=httptest.NewRecorder();h.ServeHTTP(w,req)
 if w.Code!=http.StatusSeeOther{t.Fatal(w.Code)}
 req=httptest.NewRequest(http.MethodPost,"/chamar",strings.NewReader("guiche=1"));req.Header.Set("Content-Type","application/x-www-form-urlencoded")
 w=httptest.NewRecorder();h.ServeHTTP(w,req);if w.Code!=http.StatusSeeOther{t.Fatal(w.Code,w.Body.String())}
 req=httptest.NewRequest(http.MethodGet,"/",nil);w=httptest.NewRecorder();h.ServeHTTP(w,req)
 if !strings.Contains(w.Body.String(),"chamada"){t.Fatal("senha não aparece na interface")}
}


func TestRejectCrossOriginPosts(t *testing.T) {
 q:=&Queue{}
 h:=app(q)
 for _,tc:=range []struct{name, origin, fetchSite string; want int}{
  {"outro site","https://malicioso.example","cross-site",http.StatusForbidden},
  {"outro site sem fetch metadata","http://malicioso.example","",http.StatusForbidden},
  {"navegador local","http://example.com","same-origin",http.StatusSeeOther},
  {"sem origin header","","",http.StatusSeeOther},
 } {
  t.Run(tc.name,func(t *testing.T){
   req:=httptest.NewRequest(http.MethodPost,"http://example.com/emitir",nil)
   if tc.origin!="" {req.Header.Set("Origin",tc.origin)}
   if tc.fetchSite!="" {req.Header.Set("Sec-Fetch-Site",tc.fetchSite)}
   w:=httptest.NewRecorder();h.ServeHTTP(w,req)
   if w.Code!=tc.want {t.Fatalf("resposta %d, esperada %d",w.Code,tc.want)}
  })
 }
 if len(q.List())!=2 {t.Fatalf("somente formulários permitidos devem emitir senha: %+v",q.List())}
}

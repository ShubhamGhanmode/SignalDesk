package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ShubhamGhanmode/SignalDesk/internal/auth"
	"github.com/ShubhamGhanmode/SignalDesk/internal/config"
	"github.com/ShubhamGhanmode/SignalDesk/internal/store"
	"github.com/ShubhamGhanmode/SignalDesk/internal/stream"
	"github.com/ShubhamGhanmode/SignalDesk/internal/telemetry"
	"github.com/ShubhamGhanmode/SignalDesk/web"
)

type Server struct {
	Config config.Config
	DB *store.Postgres
	Timeline *store.Cassandra
	Search *store.Search
	Cache *store.Cache
	Publisher *stream.Publisher
	Metrics *telemetry.Metrics
	HTTP *http.Client
}

func (s *Server) Handler() http.Handler {
	mux:=http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login",s.login)
	mux.Handle("POST /api/v1/auth/logout",s.session(http.HandlerFunc(s.logout)))
	mux.Handle("GET /api/v1/me",s.session(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){ respond(w,200,auth.Current(r.Context())) })))
	mux.HandleFunc("POST /api/v1/events",s.ingest)
	mux.Handle("GET /api/v1/events",s.analyst(http.HandlerFunc(s.events)))
	mux.Handle("GET /api/v1/timeline/{entity}",s.analyst(http.HandlerFunc(s.timeline)))
	mux.Handle("GET /api/v1/overview",s.session(http.HandlerFunc(s.overview)))
	mux.Handle("GET /api/v1/widgets",s.session(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){ respond(w,200,map[string]any{"version":1,"widgets":definitions(auth.Current(r.Context()).Role)}) })))
	mux.Handle("GET /api/v1/workspace",s.session(http.HandlerFunc(s.workspace)))
	mux.Handle("PUT /api/v1/workspace",s.analyst(http.HandlerFunc(s.saveWorkspace)))
	mux.Handle("GET /api/v1/investigations",s.session(http.HandlerFunc(s.investigations)))
	mux.Handle("POST /api/v1/investigations",s.analyst(http.HandlerFunc(s.saveInvestigation)))
	mux.Handle("DELETE /api/v1/investigations/{id}",s.analyst(http.HandlerFunc(s.deleteInvestigation)))
	mux.Handle("POST /api/v1/briefs",s.analyst(http.HandlerFunc(s.brief)))
	mux.HandleFunc("GET /healthz",func(w http.ResponseWriter,r *http.Request){ respond(w,200,map[string]string{"status":"ok"}) })
	mux.HandleFunc("GET /readyz",s.ready)
	mux.Handle("GET /metrics",s.Metrics)
	mux.HandleFunc("GET /api/",func(w http.ResponseWriter,r *http.Request){ fail(w,404,"endpoint not found") })
	mux.Handle("GET /",http.FileServer(http.FS(web.Files)))
	return s.middleware(mux)
}

func respond(w http.ResponseWriter,status int,value any) { w.Header().Set("Content-Type","application/json"); w.WriteHeader(status); _=json.NewEncoder(w).Encode(value) }
func fail(w http.ResponseWriter,status int,message string) { respond(w,status,map[string]string{"error":message}) }
func decode(w http.ResponseWriter,r *http.Request,value any) bool {
	r.Body=http.MaxBytesReader(w,r.Body,1<<20)
	d:=json.NewDecoder(r.Body); d.DisallowUnknownFields()
	if err:=d.Decode(value);err!=nil { fail(w,400,"invalid JSON body or unsupported fields"); return false }
	if err:=d.Decode(new(any));err!=io.EOF { fail(w,400,"request must contain one JSON object"); return false }; return true
}
func (s *Server) internal(w http.ResponseWriter,r *http.Request,err error) { slog.Error("request failed","request_id",w.Header().Get("X-Request-ID"),"error",err); fail(w,503,"service temporarily unavailable") }
type recorder struct { http.ResponseWriter; status int }
func (w *recorder) WriteHeader(status int) { w.status=status; w.ResponseWriter.WriteHeader(status) }
func (w *recorder) Write(b []byte)(int,error) { if w.status==0 { w.WriteHeader(200) }; return w.ResponseWriter.Write(b) }
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		started:=time.Now(); rw:=&recorder{ResponseWriter:w}
		rw.Header().Set("X-Request-ID",uuid.NewString()); rw.Header().Set("X-Content-Type-Options","nosniff"); rw.Header().Set("Referrer-Policy","no-referrer")
		rw.Header().Set("Content-Security-Policy","default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path,"/api/") { rw.Header().Set("Cache-Control","no-store") }
		defer func(){ if recovered:=recover();recovered!=nil { slog.Error("request panic","error",recovered); if rw.status==0 { fail(rw,500,"internal server error") } }; s.Metrics.Requests.Add(1); s.Metrics.DurationNS.Add(uint64(time.Since(started))); if rw.status>=400 { s.Metrics.Errors.Add(1) }; slog.Info("http request","method",r.Method,"path",r.URL.Path,"status",rw.status,"duration_ms",time.Since(started).Milliseconds(),"request_id",rw.Header().Get("X-Request-ID")) }()
		if r.Method!="GET" && r.Method!="HEAD" && r.URL.Path!="/api/v1/events" {
			if (r.Header.Get("Origin")!="" && r.Header.Get("Origin")!=s.Config.PublicURL) || r.Header.Get("Sec-Fetch-Site")=="cross-site" { fail(rw,403,"cross-origin request rejected"); return }
		}
		timeout:=15*time.Second; if r.URL.Path=="/api/v1/briefs" { timeout=45*time.Second }
		ctx,cancel:=context.WithTimeout(r.Context(),timeout); defer cancel(); next.ServeHTTP(rw,r.WithContext(ctx))
	})
}
func (s *Server) session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		cookie,err:=r.Cookie("signaldesk_session"); if err!=nil || len(cookie.Value)>100 { fail(w,401,"sign in required"); return }
		i,err:=s.DB.Session(r.Context(),auth.HashToken(cookie.Value)); if errors.Is(err,pgx.ErrNoRows) { fail(w,401,"session expired"); return }; if err!=nil { s.internal(w,r,err); return }
		next.ServeHTTP(w,r.WithContext(auth.WithIdentity(r.Context(),i)))
	})
}
func (s *Server) analyst(next http.Handler) http.Handler { return s.session(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){ if auth.Current(r.Context()).Role!="analyst" { fail(w,403,"analyst role required"); return }; next.ServeHTTP(w,r) })) }
func (s *Server) login(w http.ResponseWriter,r *http.Request) {
	var body struct { Email string `json:"email"`; Password string `json:"password"` }; if !decode(w,r,&body) { return }
	if len(body.Email)>254 || len(body.Password)>72 || body.Email=="" || body.Password=="" { fail(w,400,"email and password required"); return }
	ip,_,_:=net.SplitHostPort(r.RemoteAddr)
	allowed,err:=s.Cache.Allow(r.Context(),"login:"+auth.HashToken(ip),1,10,15*time.Minute); if err!=nil { s.internal(w,r,err); return }; if !allowed { fail(w,429,"too many login attempts; try again later"); return }
	i,hash,err:=s.DB.Account(r.Context(),strings.ToLower(strings.TrimSpace(body.Email)))
	if err!=nil && !errors.Is(err,pgx.ErrNoRows) { s.internal(w,r,err); return }
	if errors.Is(err,pgx.ErrNoRows) { hash="$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy" }
	valid:=auth.CheckPassword(hash,body.Password)
	if err!=nil || !valid { fail(w,401,"invalid email or password"); return }
	token,err:=auth.NewToken(); if err!=nil { s.internal(w,r,err); return }
	if err=s.DB.CreateSession(r.Context(),i.UserID,auth.HashToken(token));err!=nil { s.internal(w,r,err); return }
	http.SetCookie(w,&http.Cookie{Name:"signaldesk_session",Value:token,Path:"/",HttpOnly:true,Secure:s.Config.CookieSecure,SameSite:http.SameSiteStrictMode,MaxAge:43200})
	respond(w,200,i)
}
func (s *Server) logout(w http.ResponseWriter,r *http.Request) {
	cookie,_:=r.Cookie("signaldesk_session")
	if err:=s.DB.DeleteSession(r.Context(),auth.HashToken(cookie.Value));err!=nil { s.internal(w,r,err); return }
	http.SetCookie(w,&http.Cookie{Name:"signaldesk_session",Value:"",Path:"/",HttpOnly:true,Secure:s.Config.CookieSecure,SameSite:http.SameSiteStrictMode,MaxAge:-1}); respond(w,200,map[string]string{"status":"signed out"})
}
func (s *Server) ready(w http.ResponseWriter,r *http.Request) {
	ctx,cancel:=context.WithTimeout(r.Context(),5*time.Second); defer cancel()
	checks:=map[string]string{}; ok:=true
	// Search and ingestion dependencies do not remove working timeline routes from service.
	for name,check:=range map[string]func(context.Context)error{"postgres":s.DB.Pool.Ping,"cassandra":s.Timeline.Ping} { if err:=check(ctx);err!=nil { checks[name]="unavailable"; ok=false } else { checks[name]="ok" } }
	status:=200; if !ok { status=503 }; respond(w,status,checks)
}

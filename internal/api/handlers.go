package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ShubhamGhanmode/SignalDesk/internal/auth"
	"github.com/ShubhamGhanmode/SignalDesk/internal/model"
	"github.com/ShubhamGhanmode/SignalDesk/internal/store"
)

func (s *Server) ingest(w http.ResponseWriter,r *http.Request) {
	key:=strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer ")
	if len(key)<24 || len(key)>256 { fail(w,401,"ingestion key required"); return }
	tenant,err:=s.DB.IngestionTenant(r.Context(),auth.HashToken(key)); if errors.Is(err,pgx.ErrNoRows) { fail(w,401,"invalid ingestion key"); return }; if err!=nil { s.internal(w,r,err); return }
	var body struct { Events []model.Event `json:"events"` }; if !decode(w,r,&body) { return }
	if len(body.Events)<1 || len(body.Events)>100 { fail(w,400,"send between 1 and 100 events"); return }
	now:=time.Now().UTC().Truncate(time.Millisecond)
	for index:=range body.Events {
		e:=&body.Events[index]
		if e.TenantID!="" || !e.IngestedAt.IsZero() { fail(w,400,"tenant_id and ingested_at are assigned by the server"); return }
		e.OccurredAt=e.OccurredAt.UTC().Truncate(time.Millisecond)
		if err=e.Validate(now);err!=nil { fail(w,400,fmt.Sprintf("event %d: %s",index,err)); return }
		e.TenantID=tenant; e.IngestedAt=now
	}
	allowed,err:=s.Cache.Allow(r.Context(),"ingestion:"+tenant,len(body.Events),s.Config.IngestPerMinute,time.Minute)
	if err!=nil { s.internal(w,r,err); return }; if !allowed { w.Header().Set("Retry-After","60"); fail(w,429,"organization ingestion limit reached"); return }
	if err=s.Publisher.Publish(r.Context(),body.Events);err!=nil { s.internal(w,r,err); return }
	s.Metrics.Ingested.Add(uint64(len(body.Events))); respond(w,202,map[string]any{"accepted":len(body.Events),"status":"queued"})
}

func (s *Server) events(w http.ResponseWriter,r *http.Request) {
	f,err:=model.ParseFilter(r.URL.Query(),time.Now().UTC()); if err!=nil { fail(w,400,err.Error()); return }
	// Rounded default bounds allow nearby identical queries to share a short cache entry.
	if r.URL.Query().Get("to")=="" { f.To=f.To.Truncate(10*time.Second).Add(10*time.Second) }
	if r.URL.Query().Get("from")=="" { f.From=f.To.Add(-24*time.Hour) }
	tenant:=auth.Current(r.Context()).TenantID; key:=s.Cache.SearchKey(tenant,f)
	if b,err:=s.Cache.Get(r.Context(),key);err==nil { var cached store.SearchResult; if json.Unmarshal(b,&cached)==nil { cached.Cached=true; respond(w,200,cached); return } }
	result,err:=s.Search.Find(r.Context(),tenant,f); if err!=nil { s.internal(w,r,err); return }; b,_:=json.Marshal(result); s.Cache.Put(r.Context(),key,b); respond(w,200,result)
}

func timelineRange(values url.Values) (model.Filter,error) {
	f,err:=model.ParseFilter(values,time.Now().UTC()); if err!=nil { return f,err }
	if f.To.UTC().Truncate(24*time.Hour).Sub(f.From.UTC().Truncate(24*time.Hour))>6*24*time.Hour { return f,fmt.Errorf("timeline supports up to seven UTC calendar days") }; f.Limit=200; return f,nil
}
func (s *Server) timeline(w http.ResponseWriter,r *http.Request) {
	entity:=strings.TrimSpace(r.PathValue("entity")); if entity=="" || len(entity)>200 { fail(w,400,"invalid entity"); return }
	f,err:=timelineRange(r.URL.Query()); if err!=nil { fail(w,400,err.Error()); return }
	items,err:=s.Timeline.Timeline(r.Context(),auth.Current(r.Context()).TenantID,entity,f.From,f.To,200); if err!=nil { s.internal(w,r,err); return }
	respond(w,200,map[string]any{"entity_id":entity,"events":items,"findings":model.Findings(items),"from":f.From,"to":f.To,"limit":200,"as_of":time.Now().UTC()})
}
func (s *Server) overview(w http.ResponseWriter,r *http.Request) {
	if auth.Current(r.Context()).Role=="manager" {
		for _,key:=range []string{"q","entity","domain","severity"} { if r.URL.Query().Get(key)!="" { fail(w,403,"manager overview accepts time filters only"); return } }
	}
	f,err:=model.ParseFilter(r.URL.Query(),time.Now().UTC()); if err!=nil { fail(w,400,err.Error()); return }
	result,err:=s.Search.Overview(r.Context(),auth.Current(r.Context()).TenantID,f); if err!=nil { s.internal(w,r,err); return }; respond(w,200,result)
}
func (s *Server) workspace(w http.ResponseWriter,r *http.Request) {
	i:=auth.Current(r.Context()); b,err:=s.DB.Workspace(r.Context(),i)
	if errors.Is(err,pgx.ErrNoRows) { respond(w,200,defaultWorkspace(i.Role)); return }; if err!=nil { s.internal(w,r,err); return }; respond(w,200,b)
}
func (s *Server) saveWorkspace(w http.ResponseWriter,r *http.Request) {
	var body Workspace; if !decode(w,r,&body) { return }; i:=auth.Current(r.Context())
	if err:=body.Validate(i.Role);err!=nil { fail(w,400,err.Error()); return }; b,_:=json.Marshal(body)
	if err:=s.DB.SaveWorkspace(r.Context(),i,b);err!=nil { s.internal(w,r,err); return }; respond(w,200,body)
}
func (s *Server) investigations(w http.ResponseWriter,r *http.Request) { items,err:=s.DB.Investigations(r.Context(),auth.Current(r.Context()).TenantID); if err!=nil { s.internal(w,r,err); return }; respond(w,200,map[string]any{"investigations":items}) }
func (s *Server) saveInvestigation(w http.ResponseWriter,r *http.Request) {
	var body struct { Title string `json:"title"`; EntityID string `json:"entity_id"`; From time.Time `json:"from"`; To time.Time `json:"to"` }; if !decode(w,r,&body) { return }
	body.Title=strings.TrimSpace(body.Title); body.EntityID=strings.TrimSpace(body.EntityID)
	if body.Title=="" || len(body.Title)>160 || body.EntityID=="" || len(body.EntityID)>200 || body.From.IsZero() || body.To.IsZero() { fail(w,400,"title, entity_id, from and to are required"); return }
	values:=url.Values{"from":{body.From.Format(time.RFC3339)},"to":{body.To.Format(time.RFC3339)}}
	if _,err:=timelineRange(values);err!=nil { fail(w,400,err.Error()); return }
	item,err:=s.DB.SaveInvestigation(r.Context(),auth.Current(r.Context()),store.Investigation{Title:body.Title,EntityID:body.EntityID,From:body.From,To:body.To}); if err!=nil { s.internal(w,r,err); return }; respond(w,201,item)
}
func (s *Server) deleteInvestigation(w http.ResponseWriter,r *http.Request) { found,err:=s.DB.DeleteInvestigation(r.Context(),auth.Current(r.Context()).TenantID,r.PathValue("id")); if err!=nil { s.internal(w,r,err); return }; if !found { fail(w,404,"investigation not found"); return }; respond(w,200,map[string]string{"status":"deleted"}) }

type BriefClaim struct { Text string `json:"text"`; EvidenceIDs []string `json:"evidence_ids"` }
type Brief struct { Method string `json:"method"`; Claims []BriefClaim `json:"claims"`; ModelID string `json:"model_id,omitempty"` }
func fallbackBrief(events []model.Event) Brief {
	b:=Brief{Method:"evidence",Claims:[]BriefClaim{}}
	for _,finding:=range model.Findings(events) { if len(b.Claims)>=20 { break }; b.Claims=append(b.Claims,BriefClaim{finding.Title,finding.EvidenceIDs}) }
	if len(b.Claims)==0 { for _,e:=range events[:min(len(events),5)] { b.Claims=append(b.Claims,BriefClaim{e.Action+" on "+e.Asset,[]string{e.ID}}) } }; return b
}
func (s *Server) brief(w http.ResponseWriter,r *http.Request) {
	var body struct { EntityID string `json:"entity_id"`; From string `json:"from"`; To string `json:"to"` }; if !decode(w,r,&body) { return }
	if strings.TrimSpace(body.EntityID)=="" || len(body.EntityID)>200 { fail(w,400,"entity_id required"); return }
	f,err:=timelineRange(url.Values{"from":{body.From},"to":{body.To}}); if err!=nil { fail(w,400,err.Error()); return }
	events,err:=s.Timeline.Timeline(r.Context(),auth.Current(r.Context()).TenantID,body.EntityID,f.From,f.To,100); if err!=nil { s.internal(w,r,err); return }
	result:=fallbackBrief(events)
	if len(events)>0 && s.Config.BriefToken!="" {
		b,_:=json.Marshal(map[string]any{"events":events}); req,err:=http.NewRequestWithContext(r.Context(),"POST",s.Config.BriefURL+"/summarize",bytes.NewReader(b))
		if err==nil {
			req.Header.Set("Content-Type","application/json"); req.Header.Set("Authorization","Bearer "+s.Config.BriefToken)
			if resp,err:=s.HTTP.Do(req);err==nil {
				var candidate Brief; decodeErr:=json.NewDecoder(io.LimitReader(resp.Body,1<<20)).Decode(&candidate); _=resp.Body.Close()
				if resp.StatusCode==200 && decodeErr==nil && validBrief(candidate,events) { result=candidate }
			}
		}
	}
	respond(w,200,map[string]any{"entity_id":body.EntityID,"event_count":len(events),"brief":result})
}
func validBrief(b Brief,events []model.Event) bool {
	if (b.Method!="evidence" && b.Method!="bedrock") || len(b.Claims)==0 || len(b.Claims)>20 { return false }
	ids:=map[string]bool{}; for _,e:=range events { ids[e.ID]=true }
	for _,claim:=range b.Claims { if strings.TrimSpace(claim.Text)=="" || len(claim.Text)>2000 || len(claim.EvidenceIDs)==0 { return false }; for _,id:=range claim.EvidenceIDs { if !ids[id] { return false } } }; return true
}

package model

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	ID string `json:"event_id"`
	SchemaVersion int `json:"schema_version"`
	TenantID string `json:"tenant_id,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
	IngestedAt time.Time `json:"ingested_at,omitempty"`
	Domain string `json:"domain"`
	Action string `json:"action"`
	Severity string `json:"severity"`
	EntityID string `json:"entity_id"`
	Actor string `json:"actor"`
	Asset string `json:"asset"`
	Message string `json:"message"`
	Metadata map[string]string `json:"metadata"`
}

func (e Event) Validate(now time.Time) error {
	if id, err := uuid.Parse(e.ID); err != nil || id.String()!=e.ID { return fmt.Errorf("event_id must be a canonical lowercase UUID") }
	if e.SchemaVersion != 1 { return fmt.Errorf("schema_version must be 1") }
	if !OneOf(e.Domain, "identity", "endpoint", "cloud") { return fmt.Errorf("invalid domain") }
	if !OneOf(e.Severity, "info", "low", "medium", "high", "critical") { return fmt.Errorf("invalid severity") }
	if e.OccurredAt.IsZero() || e.OccurredAt.Before(now.Add(-30*24*time.Hour)) || e.OccurredAt.After(now.Add(5*time.Minute)) { return fmt.Errorf("occurred_at must be within the last 30 days") }
	for field, value := range map[string]string{"entity_id":e.EntityID, "action":e.Action, "actor":e.Actor, "asset":e.Asset} {
		if strings.TrimSpace(value) == "" || len(value) > 200 || strings.ContainsAny(value, "\r\n\x00") { return fmt.Errorf("invalid %s", field) }
	}
	if len(e.Message) > 2000 || len(e.Metadata) > 20 { return fmt.Errorf("event payload exceeds limits") }
	for k,v := range e.Metadata { if len(k)>64 || len(v)>500 { return fmt.Errorf("metadata exceeds limits") } }
	return nil
}

type Filter struct {
	Query string `json:"q"`
	Domain string `json:"domain"`
	Severity string `json:"severity"`
	Entity string `json:"entity"`
	From time.Time `json:"from"`
	To time.Time `json:"to"`
	Limit int `json:"limit"`
	Offset int `json:"offset"`
}

func ParseFilter(q url.Values, now time.Time) (Filter,error) {
	f:=Filter{Query:strings.TrimSpace(q.Get("q")),Domain:q.Get("domain"),Severity:q.Get("severity"),Entity:q.Get("entity"),From:now.Add(-24*time.Hour),To:now,Limit:50}
	var err error
	if q.Get("from")!="" { f.From,err=time.Parse(time.RFC3339,q.Get("from")); if err!=nil { return f,fmt.Errorf("invalid from timestamp") } }
	if q.Get("to")!="" { f.To,err=time.Parse(time.RFC3339,q.Get("to")); if err!=nil { return f,fmt.Errorf("invalid to timestamp") } }
	if q.Get("limit")!="" { f.Limit,err=strconv.Atoi(q.Get("limit")); if err!=nil { return f,fmt.Errorf("invalid limit") } }
	if q.Get("offset")!="" { f.Offset,err=strconv.Atoi(q.Get("offset")); if err!=nil { return f,fmt.Errorf("invalid offset") } }
	if f.Limit<1 || f.Limit>200 || f.Offset<0 || f.Offset+f.Limit>1000 { return f,fmt.Errorf("limit must be 1..200; offset + limit must not exceed 1000") }
	if f.From.After(f.To) || f.To.Sub(f.From)>30*24*time.Hour || f.From.Before(now.Add(-30*24*time.Hour)) || f.To.After(now.Add(5*time.Minute)) { return f,fmt.Errorf("time range must be within the last 30 days") }
	if len(f.Query)>200 || len(f.Entity)>200 { return f,fmt.Errorf("filter exceeds limits") }
	if f.Domain!="" && !OneOf(f.Domain,"identity","endpoint","cloud") { return f,fmt.Errorf("invalid domain") }
	if f.Severity!="" && !OneOf(f.Severity,"info","low","medium","high","critical") { return f,fmt.Errorf("invalid severity") }
	f.From=f.From.UTC(); f.To=f.To.UTC()
	return f,nil
}

type Finding struct { Title string `json:"title"`; Severity string `json:"severity"`; EvidenceIDs []string `json:"evidence_ids"` }

// Findings are deliberately small, explainable indicators, not a detection engine.
func Findings(events []Event) []Finding {
	result:=make([]Finding,0)
	actions:=map[string]string{"suspicious_login":"Unusual identity login","privilege_escalation":"Cloud permissions elevated","suspicious_process":"Suspicious endpoint process","sensitive_object_access":"Sensitive cloud objects accessed"}
	ids:=make([]string,0)
	domains:=map[string]bool{}
	for _,e:=range events { if title,ok:=actions[e.Action];ok { result=append(result,Finding{title,e.Severity,[]string{e.ID}}); ids=append(ids,e.ID); domains[e.Domain]=true } }
	if len(domains)>=3 { result=append(result,Finding{"Related suspicious activity spans identity, endpoint and cloud", "critical",ids}) }
	return result
}

func OneOf(value string, choices ...string) bool { for _,v:=range choices { if value==v { return true } }; return false }

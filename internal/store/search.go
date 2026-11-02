package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ShubhamGhanmode/SignalDesk/internal/config"
	"github.com/ShubhamGhanmode/SignalDesk/internal/model"
)

const eventIndex = "signaldesk-events-v1"
type Search struct { URL,User,Password string; Client *http.Client }
func NewSearch(c config.Config) *Search { return &Search{c.SearchURL,c.SearchUser,c.SearchPassword,&http.Client{Timeout:8*time.Second}} }
func (s *Search) request(ctx context.Context,method,path string,input any) ([]byte,int,error) {
	var body io.Reader
	if input!=nil { b,err:=json.Marshal(input); if err!=nil { return nil,0,err }; body=bytes.NewReader(b) }
	req,err:=http.NewRequestWithContext(ctx,method,s.URL+path,body); if err!=nil { return nil,0,err }
	req.Header.Set("Content-Type","application/json"); if s.User!="" { req.SetBasicAuth(s.User,s.Password) }
	resp,err:=s.Client.Do(req); if err!=nil { return nil,0,err }; defer resp.Body.Close()
	b,err:=io.ReadAll(io.LimitReader(resp.Body,10<<20)); if err!=nil { return nil,resp.StatusCode,err }
	if resp.StatusCode>=300 { return b,resp.StatusCode,fmt.Errorf("OpenSearch returned HTTP %d",resp.StatusCode) }
	return b,resp.StatusCode,nil
}
func (s *Search) Ping(ctx context.Context) error { _,_,err:=s.request(ctx,"GET","/",nil); return err }
func (s *Search) Init(ctx context.Context) error {
	_,status,err:=s.request(ctx,"HEAD","/"+eventIndex,nil); if err==nil { return nil }; if status!=404 { return err }
	properties:=map[string]any{}
	for _,key:=range []string{"event_id","tenant_id","domain","action","severity","entity_id","actor","asset"} { properties[key]=map[string]any{"type":"keyword","ignore_above":256} }
	properties["schema_version"]=map[string]any{"type":"integer"}; properties["message"]=map[string]any{"type":"text"}
	properties["metadata"]=map[string]any{"type":"object","enabled":false}
	for _,key:=range []string{"occurred_at","ingested_at"} { properties[key]=map[string]any{"type":"date"} }
	_,_,err=s.request(ctx,"PUT","/"+eventIndex,map[string]any{"settings":map[string]any{"number_of_shards":3,"number_of_replicas":0},"mappings":map[string]any{"dynamic":"strict","properties":properties}})
	return err
}
func (s *Search) Write(ctx context.Context,e model.Event) error { _,_,err:=s.request(ctx,"PUT","/"+eventIndex+"/_doc/"+url.PathEscape(e.TenantID+":"+e.ID),e); return err }
func searchQuery(tenant string,f model.Filter) map[string]any {
	filters:=[]any{map[string]any{"term":map[string]any{"tenant_id":tenant}},map[string]any{"range":map[string]any{"occurred_at":map[string]any{"gte":f.From,"lte":f.To}}}}
	for key,value:=range map[string]string{"domain":f.Domain,"severity":f.Severity,"entity_id":f.Entity} { if value!="" { filters=append(filters,map[string]any{"term":map[string]any{key:value}}) } }
	query:=map[string]any{"filter":filters}
	if f.Query!="" { query["must"]=[]any{map[string]any{"simple_query_string":map[string]any{"query":f.Query,"fields":[]string{"message","actor","asset","entity_id","action"},"default_operator":"and","flags":"NONE"}}} }
	return map[string]any{"bool":query}
}
type SearchResult struct {
	Events []model.Event `json:"events"`
	Total int64 `json:"total"`
	AsOf time.Time `json:"as_of"`
	Cached bool `json:"cached"`
}
func (s *Search) Find(ctx context.Context,tenant string,f model.Filter) (SearchResult,error) {
	result:=SearchResult{Events:make([]model.Event,0),AsOf:time.Now().UTC()}
	input:=map[string]any{"query":searchQuery(tenant,f),"size":f.Limit,"from":f.Offset,"track_total_hits":true,"sort":[]any{map[string]string{"occurred_at":"desc"},map[string]string{"event_id":"asc"}}}
	b,_,err:=s.request(ctx,"POST","/"+eventIndex+"/_search",input); if err!=nil { return result,err }
	var data struct { Hits struct { Total struct { Value int64 `json:"value"` } `json:"total"`; Hits []struct { Source model.Event `json:"_source"` } `json:"hits"` } `json:"hits"` }
	if err=json.Unmarshal(b,&data);err!=nil { return result,err }; result.Total=data.Hits.Total.Value
	for _,hit:=range data.Hits.Hits { result.Events=append(result.Events,hit.Source) }; return result,nil
}
type Bucket struct { Key string `json:"key"`; Count int64 `json:"doc_count"` }
type Overview struct {
	Total int64 `json:"total"`
	HighRisk int64 `json:"high_risk"`
	Entities int64 `json:"entities"`
	Severities []Bucket `json:"severities"`
	Domains []Bucket `json:"domains"`
	LatestIndexedIngestion *time.Time `json:"latest_indexed_ingestion"`
	AsOf time.Time `json:"as_of"`
}
func (s *Search) Overview(ctx context.Context,tenant string,f model.Filter) (Overview,error) {
	out:=Overview{Severities:[]Bucket{},Domains:[]Bucket{},AsOf:time.Now().UTC()}
	input:=map[string]any{"query":searchQuery(tenant,f),"size":0,"track_total_hits":true,"aggs":map[string]any{
		"severities":map[string]any{"terms":map[string]any{"field":"severity","size":5}},
		"domains":map[string]any{"terms":map[string]any{"field":"domain","size":3}},
		"entities":map[string]any{"cardinality":map[string]any{"field":"entity_id"}},
		"latest":map[string]any{"max":map[string]any{"field":"ingested_at"}},
	}}
	b,_,err:=s.request(ctx,"POST","/"+eventIndex+"/_search",input); if err!=nil { return out,err }
	var data struct {
		Hits struct { Total struct { Value int64 `json:"value"` } `json:"total"` } `json:"hits"`
		Aggs struct {
			Severities struct { Buckets []Bucket `json:"buckets"` } `json:"severities"`
			Domains struct { Buckets []Bucket `json:"buckets"` } `json:"domains"`
			Entities struct { Value int64 `json:"value"` } `json:"entities"`
			Latest struct { Value *float64 `json:"value"` } `json:"latest"`
		} `json:"aggregations"`
	}
	if err=json.Unmarshal(b,&data);err!=nil { return out,err }; out.Total=data.Hits.Total.Value; out.Entities=data.Aggs.Entities.Value
	if data.Aggs.Severities.Buckets!=nil { out.Severities=data.Aggs.Severities.Buckets }; if data.Aggs.Domains.Buckets!=nil { out.Domains=data.Aggs.Domains.Buckets }
	for _,bucket:=range out.Severities { if bucket.Key=="high" || bucket.Key=="critical" { out.HighRisk+=bucket.Count } }
	if data.Aggs.Latest.Value!=nil { t:=time.UnixMilli(int64(*data.Aggs.Latest.Value)).UTC(); out.LatestIndexedIngestion=&t }
	return out,nil
}

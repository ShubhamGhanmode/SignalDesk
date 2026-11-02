package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gocql/gocql"
	"github.com/ShubhamGhanmode/SignalDesk/internal/config"
	"github.com/ShubhamGhanmode/SignalDesk/internal/model"
)

type Cassandra struct { Session *gocql.Session }
func CassandraCluster(c config.Config) *gocql.ClusterConfig {
	cluster:=gocql.NewCluster(c.CassandraHosts...); cluster.Keyspace=c.CassandraKeyspace; cluster.Timeout=5*time.Second; cluster.ConnectTimeout=10*time.Second; cluster.Consistency=gocql.LocalQuorum
	if c.CassandraUser!="" { cluster.Authenticator=gocql.PasswordAuthenticator{Username:c.CassandraUser,Password:c.CassandraPassword} }
	if c.CassandraTLS { cluster.SslOpts=&gocql.SslOptions{EnableHostVerification:true} }
	return cluster
}
func OpenCassandra(c config.Config) (*Cassandra,error) { s,err:=CassandraCluster(c).CreateSession(); if err!=nil { return nil,err }; return &Cassandra{s},nil }
func (c *Cassandra) Close() { c.Session.Close() }
func (c *Cassandra) Ping(ctx context.Context) error { var name string; return c.Session.Query(`SELECT cluster_name FROM system.local`).WithContext(ctx).Scan(&name) }
func (c *Cassandra) Write(ctx context.Context,e model.Event) error {
	// Do not extend retention when Kafka replays an old event.
	ttl:=int(time.Until(e.IngestedAt.Add(30*24*time.Hour)).Seconds()); if ttl<=0 { return nil }
	b,err:=json.Marshal(e); if err!=nil { return err }; id,err:=gocql.ParseUUID(e.ID); if err!=nil { return err }
	return c.Session.Query(`INSERT INTO events_by_entity(tenant_id,entity_id,event_day,occurred_at,event_id,payload) VALUES (?,?,?,?,?,?) USING TTL ?`,e.TenantID,e.EntityID,e.OccurredAt.UTC().Format("2006-01-02"),e.OccurredAt,id,string(b),ttl).WithContext(ctx).Exec()
}
func (c *Cassandra) Timeline(ctx context.Context,tenant,entity string,from,to time.Time,limit int) ([]model.Event,error) {
	day:=to.UTC().Truncate(24*time.Hour); first:=from.UTC().Truncate(24*time.Hour)
	if day.Sub(first)>6*24*time.Hour { return nil,fmt.Errorf("timeline is limited to seven UTC calendar days") }
	items:=make([]model.Event,0)
	for !day.Before(first) && len(items)<limit {
		iter:=c.Session.Query(`SELECT payload FROM events_by_entity WHERE tenant_id=? AND entity_id=? AND event_day=? AND occurred_at>=? AND occurred_at<=? LIMIT ?`,tenant,entity,day.Format("2006-01-02"),from,to,limit-len(items)).WithContext(ctx).Iter()
		var payload string
		for iter.Scan(&payload) { var e model.Event; if err:=json.Unmarshal([]byte(payload),&e);err!=nil { _=iter.Close(); return nil,err }; items=append(items,e) }
		if err:=iter.Close();err!=nil { return nil,err }; day=day.Add(-24*time.Hour)
	}
	sort.Slice(items,func(i,j int)bool{ if items[i].OccurredAt.Equal(items[j].OccurredAt) { return items[i].ID<items[j].ID }; return items[i].OccurredAt.After(items[j].OccurredAt) })
	return items,nil
}

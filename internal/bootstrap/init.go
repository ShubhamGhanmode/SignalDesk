package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/ShubhamGhanmode/SignalDesk/internal/auth"
	"github.com/ShubhamGhanmode/SignalDesk/internal/config"
	"github.com/ShubhamGhanmode/SignalDesk/internal/store"
	"github.com/ShubhamGhanmode/SignalDesk/internal/stream"
	"github.com/ShubhamGhanmode/SignalDesk/migrations"
)

func Run(ctx context.Context,c config.Config,seed bool) error {
	p,err:=store.OpenPostgres(ctx,c.PostgresURL); if err!=nil { return err }; defer p.Close()
	sql,err:=migrations.Files.ReadFile("001_postgres.sql"); if err!=nil { return err }
	if _,err=p.Pool.Exec(ctx,string(sql));err!=nil { return fmt.Errorf("Postgres schema: %w",err) }
	cluster:=store.CassandraCluster(c); cluster.Keyspace="system"
	session,err:=cluster.CreateSession(); if err!=nil { return err }
	err=session.Query(`CREATE KEYSPACE IF NOT EXISTS `+c.CassandraKeyspace+` WITH replication = {'class':'NetworkTopologyStrategy','replication_factor':1}`).WithContext(ctx).Exec(); session.Close(); if err!=nil { return err }
	cs,err:=store.OpenCassandra(c); if err!=nil { return err }; defer cs.Close()
	cql,err:=migrations.Files.ReadFile("001_cassandra.cql"); if err!=nil { return err }
	if err=cs.Session.Query(string(cql)).WithContext(ctx).Exec();err!=nil { return err }
	if err=store.NewSearch(c).Init(ctx);err!=nil { return err }
	if err=stream.Init(ctx,c);err!=nil { return err }
	if seed { return Seed(ctx,p) }; return nil
}

func Seed(ctx context.Context,p *store.Postgres) error {
	password:=os.Getenv("DEMO_PASSWORD"); acmeKey:=os.Getenv("DEMO_ACME_API_KEY"); northstarKey:=os.Getenv("DEMO_NORTHSTAR_API_KEY")
	if len(password)<12 || len(password)>72 || len(acmeKey)<24 || len(northstarKey)<24 || acmeKey==northstarKey { return fmt.Errorf("seed requires DEMO_PASSWORD (12..72 characters) and two distinct DEMO_*_API_KEY values (24+ characters)") }
	hash,err:=auth.HashPassword(password); if err!=nil { return err }
	tx,err:=p.Pool.Begin(ctx); if err!=nil { return err }; defer tx.Rollback(ctx)
	for _,org:=range []struct { id,name,key string }{{"acme","Acme Security",acmeKey},{"northstar","Northstar Labs",northstarKey}} {
		if _,err=tx.Exec(ctx,`INSERT INTO organizations(id,name) VALUES ($1,$2) ON CONFLICT (id) DO NOTHING`,org.id,org.name);err!=nil { return err }
		for _,role:=range []string{"analyst","manager"} {
			email:=role+"@"+org.id+".local"
			if _,err=tx.Exec(ctx,`INSERT INTO users(id,tenant_id,email,name,password_hash,role) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (email) DO NOTHING`,uuid.NewString(),org.id,email,strings.ToUpper(role[:1])+role[1:],hash,role);err!=nil { return err }
		}
		if _,err=tx.Exec(ctx,`INSERT INTO ingestion_keys(token_hash,tenant_id,name) VALUES ($1,$2,'demo-simulator') ON CONFLICT (token_hash) DO NOTHING`,auth.HashToken(org.key),org.id);err!=nil { return err }
	}
	return tx.Commit(ctx)
}

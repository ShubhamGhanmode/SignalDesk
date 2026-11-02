package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ShubhamGhanmode/SignalDesk/internal/auth"
)

type Postgres struct { Pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, connection string) (*Postgres,error) {
	cfg,err:=pgxpool.ParseConfig(connection); if err!=nil { return nil,err }
	cfg.MaxConns=12; cfg.ConnConfig.ConnectTimeout=5*time.Second
	p,err:=pgxpool.NewWithConfig(ctx,cfg); if err!=nil { return nil,err }
	if err=p.Ping(ctx);err!=nil { p.Close(); return nil,err }
	return &Postgres{p},nil
}
func (p *Postgres) Close() { p.Pool.Close() }
func (p *Postgres) Account(ctx context.Context,email string) (auth.Identity,string,error) {
	var i auth.Identity; var hash string
	err:=p.Pool.QueryRow(ctx,`SELECT u.id,u.tenant_id,o.name,u.email,u.name,u.role,u.password_hash FROM users u JOIN organizations o ON o.id=u.tenant_id WHERE u.email=$1`,email).Scan(&i.UserID,&i.TenantID,&i.TenantName,&i.Email,&i.Name,&i.Role,&hash)
	return i,hash,err
}
func (p *Postgres) CreateSession(ctx context.Context,userID,hash string) error {
	// Bound expired session storage during ordinary login activity.
	_,err:=p.Pool.Exec(ctx,`DELETE FROM sessions WHERE expires_at < now()`); if err!=nil { return err }
	_,err=p.Pool.Exec(ctx,`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES ($1,$2,$3)`,hash,userID,time.Now().Add(12*time.Hour)); return err
}
func (p *Postgres) Session(ctx context.Context,hash string) (auth.Identity,error) {
	var i auth.Identity
	err:=p.Pool.QueryRow(ctx,`SELECT u.id,u.tenant_id,o.name,u.email,u.name,u.role FROM sessions s JOIN users u ON u.id=s.user_id JOIN organizations o ON o.id=u.tenant_id WHERE s.token_hash=$1 AND s.expires_at>now()`,hash).Scan(&i.UserID,&i.TenantID,&i.TenantName,&i.Email,&i.Name,&i.Role)
	return i,err
}
func (p *Postgres) DeleteSession(ctx context.Context,hash string) error { _,err:=p.Pool.Exec(ctx,`DELETE FROM sessions WHERE token_hash=$1`,hash); return err }
func (p *Postgres) IngestionTenant(ctx context.Context,hash string) (string,error) { var tenant string; err:=p.Pool.QueryRow(ctx,`SELECT tenant_id FROM ingestion_keys WHERE token_hash=$1`,hash).Scan(&tenant); return tenant,err }
func (p *Postgres) Workspace(ctx context.Context,i auth.Identity) (json.RawMessage,error) { var b []byte; err:=p.Pool.QueryRow(ctx,`SELECT config FROM workspaces WHERE tenant_id=$1 AND user_id=$2`,i.TenantID,i.UserID).Scan(&b); return json.RawMessage(b),err }
func (p *Postgres) SaveWorkspace(ctx context.Context,i auth.Identity,b []byte) error { _,err:=p.Pool.Exec(ctx,`INSERT INTO workspaces(tenant_id,user_id,config) VALUES ($1,$2,$3) ON CONFLICT (tenant_id,user_id) DO UPDATE SET config=EXCLUDED.config,updated_at=now()`,i.TenantID,i.UserID,b); return err }

type Investigation struct {
	ID string `json:"id"`
	Title string `json:"title"`
	EntityID string `json:"entity_id"`
	From time.Time `json:"from"`
	To time.Time `json:"to"`
	CreatedAt time.Time `json:"created_at"`
	SavedBy string `json:"saved_by"`
}
func (p *Postgres) Investigations(ctx context.Context,tenant string) ([]Investigation,error) {
	rows,err:=p.Pool.Query(ctx,`SELECT id,title,entity_id,start_at,end_at,created_at,saved_by FROM investigations WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`,tenant); if err!=nil { return nil,err }; defer rows.Close()
	items:=make([]Investigation,0)
	for rows.Next() { var item Investigation; if err:=rows.Scan(&item.ID,&item.Title,&item.EntityID,&item.From,&item.To,&item.CreatedAt,&item.SavedBy);err!=nil { return nil,err }; items=append(items,item) }
	return items,rows.Err()
}
func (p *Postgres) SaveInvestigation(ctx context.Context,i auth.Identity,item Investigation) (Investigation,error) {
	item.ID=uuid.NewString(); item.SavedBy=i.UserID
	err:=p.Pool.QueryRow(ctx,`INSERT INTO investigations(id,tenant_id,saved_by,title,entity_id,start_at,end_at) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`,item.ID,i.TenantID,i.UserID,item.Title,item.EntityID,item.From,item.To).Scan(&item.CreatedAt)
	return item,err
}
func (p *Postgres) DeleteInvestigation(ctx context.Context,tenant,id string) (bool,error) { tag,err:=p.Pool.Exec(ctx,`DELETE FROM investigations WHERE tenant_id=$1 AND id=$2`,tenant,id); return tag.RowsAffected()>0,err }

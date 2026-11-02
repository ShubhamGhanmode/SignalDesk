package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/ShubhamGhanmode/SignalDesk/internal/model"
)

type Cache struct { Client *redis.Client }
func OpenCache(connection string) (*Cache,error) { opts,err:=redis.ParseURL(connection); if err!=nil { return nil,err }; opts.DialTimeout=2*time.Second; opts.ReadTimeout=time.Second; opts.WriteTimeout=time.Second; return &Cache{redis.NewClient(opts)},nil }
func (c *Cache) Close() error { return c.Client.Close() }
func (c *Cache) SearchKey(tenant string,f model.Filter) string { b,_:=json.Marshal(f); h:=sha256.Sum256(b); return "search:"+tenant+":"+hex.EncodeToString(h[:]) }
func (c *Cache) Get(ctx context.Context,key string) ([]byte,error) { return c.Client.Get(ctx,key).Bytes() }
func (c *Cache) Put(ctx context.Context,key string,b []byte) { _=c.Client.Set(ctx,key,b,10*time.Second).Err() }
func (c *Cache) Allow(ctx context.Context,key string,amount,limit int,window time.Duration) (bool,error) {
	const script=`local n=redis.call('INCRBY',KEYS[1],ARGV[1]); if n==tonumber(ARGV[1]) then redis.call('PEXPIRE',KEYS[1],ARGV[2]) end; return n`
	n,err:=c.Client.Eval(ctx,script,[]string{"limit:"+key},amount,window.Milliseconds()).Int64()
	return n<=int64(limit),err
}

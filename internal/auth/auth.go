package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

type Identity struct {
	UserID string `json:"user_id"`
	TenantID string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	Email string `json:"email"`
	Name string `json:"name"`
	Role string `json:"role"`
}
type contextKey struct{}
func WithIdentity(ctx context.Context,i Identity) context.Context { return context.WithValue(ctx,contextKey{},i) }
func Current(ctx context.Context) Identity { i,_:=ctx.Value(contextKey{}).(Identity); return i }
func HashToken(token string) string { h:=sha256.Sum256([]byte(token)); return hex.EncodeToString(h[:]) }
func NewToken() (string,error) { var b [32]byte; if _,err:=rand.Read(b[:]);err!=nil { return "",err }; return base64.RawURLEncoding.EncodeToString(b[:]),nil }
func HashPassword(password string) (string,error) { b,err:=bcrypt.GenerateFromPassword([]byte(password),bcrypt.DefaultCost); return string(b),err }
func CheckPassword(hash,password string) bool { return bcrypt.CompareHashAndPassword([]byte(hash),[]byte(password))==nil }

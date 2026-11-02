package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ShubhamGhanmode/SignalDesk/internal/api"
	"github.com/ShubhamGhanmode/SignalDesk/internal/bootstrap"
	"github.com/ShubhamGhanmode/SignalDesk/internal/config"
	"github.com/ShubhamGhanmode/SignalDesk/internal/store"
	"github.com/ShubhamGhanmode/SignalDesk/internal/stream"
	"github.com/ShubhamGhanmode/SignalDesk/internal/telemetry"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout,nil)))
	ctx,stop:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM); defer stop()
	if err:=run(ctx);err!=nil && !errors.Is(err,context.Canceled) { slog.Error("service stopped","error",err); os.Exit(1) }
}
func run(ctx context.Context) error {
	c,err:=config.Load(); if err!=nil { return err }
	if len(os.Args)<2 { return fmt.Errorf("usage: signaldesk api | worker timeline | worker search | init [--seed]") }
	metrics:=&telemetry.Metrics{}
	switch os.Args[1] {
	case "init":
		initCtx,cancel:=context.WithTimeout(ctx,2*time.Minute); defer cancel()
		seed:=len(os.Args)>2 && os.Args[2]=="--seed"
		if err:=bootstrap.Run(initCtx,c,seed);err!=nil { return err }; slog.Info("schema initialization complete","seed",seed); return nil
	case "api":
		p,err:=store.OpenPostgres(ctx,c.PostgresURL); if err!=nil { return err }; defer p.Close()
		cs,err:=store.OpenCassandra(c); if err!=nil { return err }; defer cs.Close()
		cache,err:=store.OpenCache(c.RedisURL); if err!=nil { return err }; defer cache.Close()
		publisher:=stream.NewPublisher(c); defer publisher.Close()
		s:=&api.Server{Config:c,DB:p,Timeline:cs,Search:store.NewSearch(c),Cache:cache,Publisher:publisher,Metrics:metrics,HTTP:&http.Client{Timeout:35*time.Second}}
		return serve(ctx,c.Addr,s.Handler())
	case "worker":
		if len(os.Args)!=3 || (os.Args[2]!="timeline" && os.Args[2]!="search") { return fmt.Errorf("worker projection must be timeline or search") }
		name:=os.Args[2]; var sink stream.Projection
		if name=="timeline" { cs,err:=store.OpenCassandra(c); if err!=nil { return err }; defer cs.Close(); sink=cs } else { sink=store.NewSearch(c) }
		workerCtx,cancel:=context.WithCancel(ctx); defer cancel()
		mux:=http.NewServeMux(); mux.Handle("GET /metrics",metrics); mux.HandleFunc("GET /healthz",func(w http.ResponseWriter,r *http.Request){ w.WriteHeader(200) })
		errs:=make(chan error,2)
		go func(){ errs<-serve(workerCtx,c.Addr,mux) }()
		go func(){ errs<-stream.Run(workerCtx,c,name,sink,metrics) }()
		err=<-errs; cancel(); <-errs; return err
	default:return fmt.Errorf("unknown command: %s",os.Args[1])
	}
}
func serve(ctx context.Context,addr string,handler http.Handler) error {
	srv:=&http.Server{Addr:addr,Handler:handler,ReadHeaderTimeout:5*time.Second,ReadTimeout:20*time.Second,WriteTimeout:60*time.Second,IdleTimeout:60*time.Second,MaxHeaderBytes:32<<10}
	done:=make(chan struct{})
	go func(){ select { case <-ctx.Done(): shutdownCtx,cancel:=context.WithTimeout(context.Background(),10*time.Second); defer cancel(); _=srv.Shutdown(shutdownCtx); case <-done: } }()
	slog.Info("HTTP server listening","address",addr)
	err:=srv.ListenAndServe(); close(done); if errors.Is(err,http.ErrServerClosed) { return nil }; return err
}

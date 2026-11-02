package stream

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/ShubhamGhanmode/SignalDesk/internal/config"
	"github.com/ShubhamGhanmode/SignalDesk/internal/model"
	"github.com/ShubhamGhanmode/SignalDesk/internal/telemetry"
)

func Dialer(c config.Config) *kafka.Dialer {
	d:=&kafka.Dialer{Timeout:10*time.Second,DualStack:true}
	if c.KafkaTLS { d.TLS=&tls.Config{MinVersion:tls.VersionTLS12} }
	if c.KafkaUser!="" { d.SASLMechanism=plain.Mechanism{Username:c.KafkaUser,Password:c.KafkaPassword} }
	return d
}
func writer(c config.Config,topic string) *kafka.Writer {
	d:=Dialer(c)
	return &kafka.Writer{Addr:kafka.TCP(c.KafkaBrokers...),Topic:topic,Balancer:&kafka.Hash{},RequiredAcks:kafka.RequireAll,Async:false,BatchTimeout:20*time.Millisecond,WriteTimeout:8*time.Second,ReadTimeout:8*time.Second,MaxAttempts:3,Transport:&kafka.Transport{TLS:d.TLS,SASL:d.SASLMechanism}}
}
type Publisher struct { Writer *kafka.Writer }
func NewPublisher(c config.Config) *Publisher { return &Publisher{writer(c,c.KafkaTopic)} }
func (p *Publisher) Close() error { return p.Writer.Close() }
func (p *Publisher) Publish(ctx context.Context,events []model.Event) error {
	messages:=make([]kafka.Message,0,len(events))
	for _,e:=range events { b,err:=json.Marshal(e); if err!=nil { return err }; messages=append(messages,kafka.Message{Key:[]byte(e.TenantID+":"+e.EntityID),Value:b,Time:e.IngestedAt}) }
	return p.Writer.WriteMessages(ctx,messages...)
}
func Init(ctx context.Context,c config.Config) error {
	conn,err:=Dialer(c).DialContext(ctx,"tcp",c.KafkaBrokers[0]); if err!=nil { return err }; defer conn.Close()
	controller,err:=conn.Controller(); if err!=nil { return err }
	admin,err:=Dialer(c).DialContext(ctx,"tcp",net.JoinHostPort(controller.Host,strconv.Itoa(controller.Port))); if err!=nil { return err }; defer admin.Close()
	for _,topic:=range []string{c.KafkaTopic,c.KafkaDLT} {
		err=admin.CreateTopics(kafka.TopicConfig{Topic:topic,NumPartitions:c.KafkaPartitions,ReplicationFactor:c.KafkaReplication,ConfigEntries:[]kafka.ConfigEntry{{ConfigName:"retention.ms",ConfigValue:"2592000000"}}})
		if err!=nil && err!=kafka.TopicAlreadyExists { return err }
	}
	return nil
}
func Ping(ctx context.Context,c config.Config) error { conn,err:=Dialer(c).DialContext(ctx,"tcp",c.KafkaBrokers[0]); if err!=nil { return err }; defer conn.Close(); _,err=conn.Brokers(); return err }

type Projection interface { Write(context.Context,model.Event) error }

func Run(ctx context.Context,c config.Config,name string,sink Projection,metrics *telemetry.Metrics) error {
	r:=kafka.NewReader(kafka.ReaderConfig{Brokers:c.KafkaBrokers,Topic:c.KafkaTopic,GroupID:"signaldesk-"+name+"-v1",Dialer:Dialer(c),MinBytes:1,MaxBytes:2<<20,MaxWait:time.Second,CommitInterval:0,StartOffset:kafka.FirstOffset})
	defer r.Close(); dead:=writer(c,c.KafkaDLT); defer dead.Close()
	for {
		message,err:=r.FetchMessage(ctx); if err!=nil { return err }
		var event model.Event
		decodeErr:=json.Unmarshal(message.Value,&event)
		if decodeErr==nil {
			if event.TenantID=="" || event.IngestedAt.IsZero() { decodeErr=fmt.Errorf("missing tenant or ingestion timestamp") } else if time.Since(event.IngestedAt)<30*24*time.Hour { decodeErr=event.Validate(time.Now().UTC()) }
		}
		operation:=func()error { if time.Since(event.IngestedAt)>=30*24*time.Hour { return nil }; return sink.Write(ctx,event) }
		if decodeErr!=nil {
			envelope,_:=json.Marshal(map[string]any{"source_topic":message.Topic,"partition":message.Partition,"offset":message.Offset,"projection":name,"error":decodeErr.Error(),"original_base64":message.Value,"failed_at":time.Now().UTC()})
			operation=func()error { return dead.WriteMessages(ctx,kafka.Message{Key:message.Key,Value:envelope}) }
		}
		for attempt:=0;;attempt++ {
			if err=operation();err==nil { break }
			metrics.Retries.Add(1); slog.Warn("projection retry","projection",name,"partition",message.Partition,"offset",message.Offset,"error",err)
			delay:=time.Duration(1<<min(attempt,4))*500*time.Millisecond
			select { case <-ctx.Done():return ctx.Err(); case <-time.After(delay): }
		}
		if err=r.CommitMessages(ctx,message);err!=nil { return fmt.Errorf("commit %s offset: %w",name,err) }
		if decodeErr!=nil { metrics.DeadLetters.Add(1) } else { metrics.Processed.Add(1); metrics.LastProcessed.Store(time.Now().Unix()) }
	}
}

package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Addr, PublicURL, PostgresURL, RedisURL string
	CassandraHosts []string
	CassandraKeyspace, CassandraUser, CassandraPassword string
	CassandraTLS bool
	KafkaBrokers []string
	KafkaTopic, KafkaDLT, KafkaUser, KafkaPassword string
	KafkaTLS bool
	KafkaPartitions, KafkaReplication int
	SearchURL, SearchUser, SearchPassword string
	BriefURL, BriefToken string
	CookieSecure bool
	IngestPerMinute int
}

func Load() (Config, error) {
	c := Config{
		Addr: env("HTTP_ADDR", ":8080"), PublicURL: strings.TrimRight(env("PUBLIC_URL", "http://localhost:8080"), "/"),
		PostgresURL: env("POSTGRES_URL", "postgres://signaldesk:signaldesk@localhost:5432/signaldesk?sslmode=disable"),
		RedisURL: env("REDIS_URL", "redis://localhost:6379/0"),
		CassandraHosts: strings.Split(env("CASSANDRA_HOSTS", "localhost:9042"), ","),
		CassandraKeyspace: env("CASSANDRA_KEYSPACE", "signaldesk"),
		CassandraUser: os.Getenv("CASSANDRA_USERNAME"), CassandraPassword: os.Getenv("CASSANDRA_PASSWORD"),
		CassandraTLS: env("CASSANDRA_TLS", "false") == "true",
		KafkaBrokers: strings.Split(env("KAFKA_BROKERS", "localhost:9092"), ","),
		KafkaTopic: env("KAFKA_TOPIC", "security.events.v1"), KafkaDLT: env("KAFKA_DLT", "security.events.dead.v1"),
		KafkaUser: os.Getenv("KAFKA_USERNAME"), KafkaPassword: os.Getenv("KAFKA_PASSWORD"), KafkaTLS: env("KAFKA_TLS", "false") == "true",
		KafkaPartitions: number("KAFKA_PARTITIONS", 6), KafkaReplication: number("KAFKA_REPLICATION", 1),
		SearchURL: strings.TrimRight(env("OPENSEARCH_URL", "http://localhost:9200"), "/"),
		SearchUser: os.Getenv("OPENSEARCH_USERNAME"), SearchPassword: os.Getenv("OPENSEARCH_PASSWORD"),
		BriefURL: strings.TrimRight(env("BRIEF_URL", "http://localhost:8090"), "/"), BriefToken: os.Getenv("BRIEF_TOKEN"),
		CookieSecure: env("COOKIE_SECURE", "false") == "true", IngestPerMinute: number("INGEST_PER_MINUTE", 6000),
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`).MatchString(c.CassandraKeyspace) {
		return c, fmt.Errorf("invalid CASSANDRA_KEYSPACE")
	}
	for _, raw := range []string{c.PublicURL, c.SearchURL, c.BriefURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") { return c, fmt.Errorf("invalid HTTP URL: %s", raw) }
	}
	if c.KafkaUser != "" && !c.KafkaTLS { return c, fmt.Errorf("KAFKA_TLS is required with SASL credentials") }
	if c.KafkaPartitions < 1 || c.KafkaReplication < 1 || c.IngestPerMinute < 1 { return c, fmt.Errorf("numeric limits must be positive") }
	return c, nil
}

func env(key, fallback string) string { if value := os.Getenv(key); value != "" { return value }; return fallback }
func number(key string, fallback int) int { value, err := strconv.Atoi(env(key, strconv.Itoa(fallback))); if err != nil { return 0 }; return value }

package telemetry

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Metrics struct {
	Requests atomic.Uint64
	Errors atomic.Uint64
	Ingested atomic.Uint64
	Processed atomic.Uint64
	Retries atomic.Uint64
	DeadLetters atomic.Uint64
	DurationNS atomic.Uint64
	LastProcessed atomic.Int64
}
func (m *Metrics) ServeHTTP(w http.ResponseWriter,r *http.Request) {
	w.Header().Set("Content-Type","text/plain; version=0.0.4")
	fmt.Fprintf(w,"# TYPE signaldesk_http_requests_total counter\nsignaldesk_http_requests_total %d\n# TYPE signaldesk_http_errors_total counter\nsignaldesk_http_errors_total %d\n# TYPE signaldesk_http_duration_seconds_sum counter\nsignaldesk_http_duration_seconds_sum %.6f\n# TYPE signaldesk_ingested_events_total counter\nsignaldesk_ingested_events_total %d\n# TYPE signaldesk_processed_events_total counter\nsignaldesk_processed_events_total %d\n# TYPE signaldesk_projection_retries_total counter\nsignaldesk_projection_retries_total %d\n# TYPE signaldesk_dead_letters_total counter\nsignaldesk_dead_letters_total %d\n# TYPE signaldesk_last_processed_timestamp_seconds gauge\nsignaldesk_last_processed_timestamp_seconds %d\n",m.Requests.Load(),m.Errors.Load(),float64(m.DurationNS.Load())/float64(time.Second),m.Ingested.Load(),m.Processed.Load(),m.Retries.Load(),m.DeadLetters.Load(),m.LastProcessed.Load())
}

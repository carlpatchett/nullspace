// Package telemetry provides lightweight OpenTelemetry-compatible tracing with JSONL file logging.
package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SpanRecord represents the serialized JSONL format for an OpenTelemetry span.
type SpanRecord struct {
	Name       string         `json:"name"`
	TraceID    string         `json:"trace_id"`
	SpanID     string         `json:"span_id"`
	StartNS    int64          `json:"start_ns"`
	EndNS      int64          `json:"end_ns"`
	DurationMS float64        `json:"duration_ms"`
	Attributes map[string]any `json:"attributes"`
}

// Span represents an active in-flight span.
type Span struct {
	name       string
	traceID    string
	spanID     string
	startNS    int64
	attributes map[string]any
	mu         sync.Mutex
	tracer     *Telemetry
	ended      bool
}

// SetAttribute adds or updates an attribute on the span.
func (s *Span) SetAttribute(key string, val any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attributes == nil {
		s.attributes = make(map[string]any)
	}
	s.attributes[fmt.Sprintf("null_space.%s", key)] = val
}

// End finishes the span and writes the record to the JSONL trace log.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	endNS := time.Now().UnixNano()
	durationMS := float64(endNS-s.startNS) / 1e6

	rec := SpanRecord{
		Name:       s.name,
		TraceID:    s.traceID,
		SpanID:     s.spanID,
		StartNS:    s.startNS,
		EndNS:      endNS,
		DurationMS: mathRound3(durationMS),
		Attributes: s.attributes,
	}
	s.mu.Unlock()

	if s.tracer != nil {
		s.tracer.recordSpan(rec)
	}
}

func mathRound3(val float64) float64 {
	return float64(int64(val*1000+0.5)) / 1000
}

// Config specifies configuration for Telemetry.
type Config struct {
	ServiceName  string
	TracePath    string
	OTLPEndpoint string
}

// Telemetry manages span creation and persistent logging.
type Telemetry struct {
	serviceName  string
	tracePath    string
	otlpEndpoint string
	mu           sync.Mutex
	file         *os.File
}

// New initializes Telemetry with the given config.
func New(cfg Config) *Telemetry {
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "nullspace"
	}

	tracePath := cfg.TracePath
	if tracePath == "" {
		tracePath = os.Getenv("NULLSPACE_TRACE_PATH")
	}
	if tracePath == "" {
		tracePath = "null_space_trace.jsonl"
	}

	otlpEndpoint := cfg.OTLPEndpoint
	if otlpEndpoint == "" {
		otlpEndpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}

	// Ensure parent dir exists
	dir := filepath.Dir(tracePath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}

	f, err := os.OpenFile(tracePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// Non-fatal, trace file unavailable
		f = nil
	}

	return &Telemetry{
		serviceName:  serviceName,
		tracePath:    tracePath,
		otlpEndpoint: otlpEndpoint,
		file:         f,
	}
}

// StartSpan creates and begins a new span.
func (t *Telemetry) StartSpan(name string, attrs ...Attr) *Span {
	startNS := time.Now().UnixNano()
	traceID := generateTraceID()
	spanID := generateSpanID()

	attrMap := make(map[string]any, len(attrs))
	for _, a := range attrs {
		attrMap[fmt.Sprintf("null_space.%s", a.Key)] = a.Val
	}

	return &Span{
		name:       name,
		traceID:    traceID,
		spanID:     spanID,
		startNS:    startNS,
		attributes: attrMap,
		tracer:     t,
	}
}

// Attr is a key-value pair for span attributes.
type Attr struct {
	Key string
	Val any
}

// A creates an Attr helper.
func A(key string, val any) Attr {
	return Attr{Key: key, Val: val}
}

func (t *Telemetry) recordSpan(rec SpanRecord) {
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	data = append(data, '\n')

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.file != nil {
		_, _ = t.file.Write(data)
	}
}

// Close flushes and closes the telemetry trace file.
func (t *Telemetry) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.file != nil {
		err := t.file.Close()
		t.file = nil
		return err
	}
	return nil
}

func generateTraceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func generateSpanID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTelemetrySpanJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "test_trace.jsonl")

	tel := New(Config{
		ServiceName: "nullspace-test",
		TracePath:   tracePath,
	})
	defer tel.Close()

	// Span 1
	span1 := tel.StartSpan("prune_search_space", A("query", "ExponentialBackoffRetry"))
	span1.SetAttribute("candidate_dirs", 2)
	span1.SetAttribute("pruned_dirs", 18)
	span1.SetAttribute("pruned_percent", 90.0)
	time.Sleep(2 * time.Millisecond)
	span1.End()

	// Span 2
	span2 := tel.StartSpan("reindex_workspace", A("indexed_files", 55))
	span2.End()

	_ = tel.Close()

	// Read and verify JSONL file
	file, err := os.Open(tracePath)
	if err != nil {
		t.Fatalf("failed to open trace file: %v", err)
	}
	defer file.Close()

	var records []SpanRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var rec SpanRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("failed to parse span record: %v", err)
		}
		records = append(records, rec)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	r1 := records[0]
	if r1.Name != "prune_search_space" {
		t.Errorf("expected name 'prune_search_space', got %q", r1.Name)
	}
	if len(r1.TraceID) != 32 {
		t.Errorf("expected 32-char hex trace_id, got %q", r1.TraceID)
	}
	if len(r1.SpanID) != 16 {
		t.Errorf("expected 16-char hex span_id, got %q", r1.SpanID)
	}
	if r1.DurationMS < 1.0 {
		t.Errorf("expected duration >= 1ms, got %f", r1.DurationMS)
	}
	if r1.Attributes["null_space.query"] != "ExponentialBackoffRetry" {
		t.Errorf("expected query attribute, got %v", r1.Attributes["null_space.query"])
	}
}

func TestTelemetryConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "concurrent_trace.jsonl")

	tel := New(Config{
		ServiceName: "nullspace-test",
		TracePath:   tracePath,
	})
	defer tel.Close()

	var wg sync.WaitGroup
	workers := 16
	spansPerWorker := 50

	for w := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := range spansPerWorker {
				s := tel.StartSpan("worker_task", A("worker", workerID), A("iter", i))
				s.SetAttribute("status", "ok")
				s.End()
			}
		}(w)
	}

	wg.Wait()
	_ = tel.Close()

	// Verify all records were written
	file, err := os.Open(tracePath)
	if err != nil {
		t.Fatalf("failed to open trace file: %v", err)
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}

	expectedCount := workers * spansPerWorker
	if count != expectedCount {
		t.Fatalf("expected %d span records, got %d", expectedCount, count)
	}
}

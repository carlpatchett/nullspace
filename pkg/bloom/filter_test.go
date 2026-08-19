package bloom

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

func TestMurmur3_128(t *testing.T) {
	// Determinism check
	data := []byte("ExponentialBackoffRetryStrategy")
	h1a, h2a := Murmur3_128(data, 0)
	h1b, h2b := Murmur3_128(data, 0)
	if h1a != h1b || h2a != h2b {
		t.Fatalf("Murmur3_128 non-deterministic: (%x,%x) != (%x,%x)", h1a, h2a, h1b, h2b)
	}

	// Different seeds produce different hashes
	h1c, h2c := Murmur3_128(data, 12345)
	if h1a == h1c && h2a == h2c {
		t.Fatalf("different seeds produced identical hashes")
	}

	// Different inputs produce different hashes
	h1d, h2d := Murmur3_128([]byte("ExponentialBackoffRetryStrategy2"), 0)
	if h1a == h1d && h2a == h2d {
		t.Fatalf("different inputs produced identical hashes")
	}
}

func TestBloomFilterBasic(t *testing.T) {
	f := New()

	tokens := []string{
		"UserService",
		"getUserById",
		"db_connection_pool",
		"MAX_RETRIES",
		"handleHttpWebhook",
	}

	for _, tok := range tokens {
		if f.ContainsString(tok) {
			t.Fatalf("uninserted token %q reported as present before insertion", tok)
		}
		f.AddString(tok)
		if !f.ContainsString(tok) {
			t.Fatalf("inserted token %q not found after insertion", tok)
		}
	}

	// Token that definitely wasn't added
	if f.ContainsString("DefinitelyNonExistentToken_XYZ_99999") {
		t.Fatalf("non-existent token found in sparse filter")
	}
}

func TestBloomFilterZeroFalseNegatives(t *testing.T) {
	f := NewWithConfig(Config{
		NumBits:   32768,
		HashCount: 4,
	})

	r := rand.New(rand.NewPCG(42, 100))
	n := 2000
	inserted := make([]string, n)

	for i := range n {
		inserted[i] = fmt.Sprintf("token_%d_%d", i, r.Uint64())
		f.AddString(inserted[i])
	}

	// Zero false negatives: EVERY single inserted token MUST return true.
	for i, tok := range inserted {
		if !f.ContainsString(tok) {
			t.Fatalf("false negative at index %d: token %q returned false", i, tok)
		}
	}
}

func TestBloomFilterFalsePositiveRate(t *testing.T) {
	// For m=16384, k=4, n=500:
	// Theoretical FPR ≈ (1 - e^(-4*500/16384))^4 ≈ 0.00017 (0.017%)
	f := NewWithConfig(Config{
		NumBits:   16384,
		HashCount: 4,
	})

	r := rand.New(rand.NewPCG(1337, 2026))
	n := 500
	for i := range n {
		f.AddString(fmt.Sprintf("inserted_var_%d_%d", i, r.Uint64()))
	}

	// Test 10,000 non-inserted tokens
	testCount := 10000
	falsePositives := 0
	for i := range testCount {
		query := fmt.Sprintf("non_existent_var_%d_%d", i, r.Uint64())
		if f.ContainsString(query) {
			falsePositives++
		}
	}

	empiricalFPR := float64(falsePositives) / float64(testCount)
	t.Logf("Empirical FPR for n=%d, m=16384, k=4: %d/%d (%.4f%%)", n, falsePositives, testCount, empiricalFPR*100)

	// Empirical FPR should be <= 1%
	if empiricalFPR > 0.01 {
		t.Fatalf("Empirical FPR %.4f exceeds 1%% threshold", empiricalFPR)
	}
}

func TestBloomFilterUnion(t *testing.T) {
	f1 := New()
	f2 := New()

	set1 := []string{"tokenA", "tokenB", "tokenC"}
	set2 := []string{"tokenC", "tokenD", "tokenE"}

	for _, s := range set1 {
		f1.AddString(s)
	}
	for _, s := range set2 {
		f2.AddString(s)
	}

	// Clone f1 before union
	unionFilter := f1.Clone()
	if err := unionFilter.Union(f2); err != nil {
		t.Fatalf("Union failed: %v", err)
	}

	// All tokens from set1 and set2 must be contained in unionFilter
	allTokens := append(set1, set2...)
	for _, tok := range allTokens {
		if !unionFilter.ContainsString(tok) {
			t.Fatalf("token %q missing from union filter", tok)
		}
	}

	// Token in neither set should not be present
	if unionFilter.ContainsString("tokenNotInEitherSet_12345") {
		t.Fatalf("unrelated token present in union filter")
	}
}

func TestBloomFilterIntersect(t *testing.T) {
	f1 := New()
	f2 := New()

	f1.AddString("common")
	f1.AddString("only_in_1")

	f2.AddString("common")
	f2.AddString("only_in_2")

	intersectFilter := f1.Clone()
	if err := intersectFilter.Intersect(f2); err != nil {
		t.Fatalf("Intersect failed: %v", err)
	}

	if !intersectFilter.ContainsString("common") {
		t.Fatalf("common token not found in intersection")
	}
}

func TestBloomFilterSerialization(t *testing.T) {
	f := New()
	tokens := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for _, tok := range tokens {
		f.AddString(tok)
	}

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	f2 := New()
	if err := f2.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	for _, tok := range tokens {
		if !f2.ContainsString(tok) {
			t.Fatalf("token %q not found in deserialized filter", tok)
		}
	}

	if f.BitCount() != f2.BitCount() {
		t.Fatalf("bit counts differ: %d vs %d", f.BitCount(), f2.BitCount())
	}
}

func TestBloomFilterConcurrency(t *testing.T) {
	f := New()
	var wg sync.WaitGroup

	numWriters := 8
	numReaders := 16
	tokensPerWriter := 100

	for w := range numWriters {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := range tokensPerWriter {
				tok := fmt.Sprintf("worker_%d_token_%d", workerID, i)
				f.AddString(tok)
				time.Sleep(10 * time.Microsecond)
			}
		}(w)
	}

	for r := range numReaders {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for range 200 {
				tok := fmt.Sprintf("worker_%d_token_%d", readerID%numWriters, rand.IntN(tokensPerWriter))
				f.ContainsString(tok)
				f.FillRatio()
				f.BitCount()
			}
		}(r)
	}

	wg.Wait()
}

func BenchmarkBloomAdd(b *testing.B) {
	f := New()
	token := []byte("benchmarkTokenFunctionName123")
	b.ResetTimer()
	for range b.N {
		f.Add(token)
	}
}

func BenchmarkBloomContains(b *testing.B) {
	f := New()
	token := []byte("benchmarkTokenFunctionName123")
	f.Add(token)
	b.ResetTimer()
	for range b.N {
		f.Contains(token)
	}
}

func BenchmarkBloomUnion(b *testing.B) {
	f1 := New()
	f2 := New()
	for i := range 100 {
		f1.AddString(fmt.Sprintf("tok_1_%d", i))
		f2.AddString(fmt.Sprintf("tok_2_%d", i))
	}

	b.ResetTimer()
	for range b.N {
		_ = f1.Union(f2)
	}
}

func BenchmarkMurmur3_128(b *testing.B) {
	data := []byte("benchmarkFunctionCallWithLongIdentifierName_12345")
	b.ResetTimer()
	for range b.N {
		_, _ = Murmur3_128(data, 0)
	}
}

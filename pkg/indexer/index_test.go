package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"null-space/pkg/telemetry"
)

func createTestWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Directory tree:
	// root/
	//   services/
	//     payments/
	//       retry.py
	//       stripe.go
	//     auth/
	//       jwt.go
	//       oauth.ts
	//   tools/
	//     mailer.cs
	//     cleanup.go
	//   docs/
	//     readme.md

	files := map[string]string{
		"services/payments/retry.py": `
class ExponentialBackoffRetry:
    def __init__(self, max_retries: int = 5):
        self.max_retries = max_retries
        self.payment_gateway_url = "https://api.stripe.com/v1"
`,
		"services/payments/stripe.go": `
package payments

type StripePaymentProcessor struct {
    ApiKey string
    WebhookSecret string
}
`,
		"services/auth/jwt.go": `
package auth

type JWTTokenManager struct {
    SigningKey []byte
    Issuer string
}
`,
		"services/auth/oauth.ts": `
export class OAuth2Client {
    private clientId: string;
    private clientSecret: string;
}
`,
		"tools/mailer.cs": `
namespace Tools;
public class MailerService {
    public void SendInvoice(string email) {}
}
`,
		"tools/cleanup.go": `
package tools

func CleanupTempDatabase() error {
    return nil
}
`,
		"docs/readme.md": `
# Documentation
Null space Bloom filter indexer documentation.
`,
	}

	for relPath, content := range files {
		fullPath := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}
	}

	return root
}

func TestWorkspaceIndexPrune(t *testing.T) {
	root := createTestWorkspace(t)
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	tel := telemetry.New(telemetry.Config{TracePath: tracePath})
	defer tel.Close()

	cfg := DefaultConfig(root)
	cfg.Telemetry = tel
	idx, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	reindexRes, err := idx.Build()
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	if reindexRes.TotalFiles != 7 {
		t.Fatalf("expected 7 files, got %d", reindexRes.TotalFiles)
	}

	// 1. Search for token only present in payments/retry.py
	res, err := idx.PruneSearchSpace("ExponentialBackoffRetry", false)
	if err != nil {
		t.Fatalf("PruneSearchSpace failed: %v", err)
	}

	if res.CandidateDirCount != 1 {
		t.Fatalf("expected exactly 1 candidate dir, got %d: %v", res.CandidateDirCount, res.CandidateDirectories)
	}
	if res.CandidateDirectories[0].Path != "services/payments" {
		t.Errorf("expected candidate dir 'services/payments', got %q", res.CandidateDirectories[0].Path)
	}
	if len(res.CandidateDirectories[0].CandidateFiles) != 1 || res.CandidateDirectories[0].CandidateFiles[0] != "services/payments/retry.py" {
		t.Errorf("expected candidate file 'services/payments/retry.py', got %v", res.CandidateDirectories[0].CandidateFiles)
	}

	// Pruned dirs must include auth, tools, docs, .
	if res.PrunedPercent < 70.0 {
		t.Errorf("expected >70%% pruned dirs, got %.1f%%", res.PrunedPercent)
	}

	// 2. Search for non-existent token: 100% of directories must be pruned
	resNone, err := idx.PruneSearchSpace("NonExistentSymbol_XYZ_12345", false)
	if err != nil {
		t.Fatalf("PruneSearchSpace failed: %v", err)
	}
	if resNone.CandidateDirCount != 0 {
		t.Errorf("expected 0 candidate dirs for non-existent token, got %d", resNone.CandidateDirCount)
	}
	if resNone.PrunedPercent != 100.0 {
		t.Errorf("expected 100%% pruned dirs, got %.1f%%", resNone.PrunedPercent)
	}

	// 3. Search for token in tools/mailer.cs
	resMailer, err := idx.PruneSearchSpace("MailerService", false)
	if err != nil {
		t.Fatalf("PruneSearchSpace failed: %v", err)
	}
	if resMailer.CandidateDirCount != 1 || resMailer.CandidateDirectories[0].Path != "tools" {
		t.Errorf("expected candidate dir 'tools', got %v", resMailer.CandidateDirectories)
	}
}

func TestWorkspaceCheckToken(t *testing.T) {
	root := createTestWorkspace(t)
	cfg := DefaultConfig(root)

	idx, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	// Direct file check
	res, err := idx.CheckToken("JWTTokenManager", "services/auth/jwt.go")
	if err != nil {
		t.Fatalf("CheckToken failed: %v", err)
	}
	if !res.Present {
		t.Errorf("expected JWTTokenManager to be present in jwt.go")
	}

	resNegative, err := idx.CheckToken("NonExistentToken", "services/auth/jwt.go")
	if err != nil {
		t.Fatalf("CheckToken failed: %v", err)
	}
	if resNegative.Present {
		t.Errorf("expected NonExistentToken to not be present")
	}
	if !resNegative.Definitive {
		t.Errorf("expected negative check to be definitive")
	}

	// Subtree check
	resSubtree, err := idx.CheckToken("OAuth2Client", "services/auth")
	if err != nil {
		t.Fatalf("CheckToken failed: %v", err)
	}
	if !resSubtree.Present {
		t.Errorf("expected OAuth2Client to be present in services/auth subtree")
	}
}

func TestWorkspaceStats(t *testing.T) {
	root := createTestWorkspace(t)
	cfg := DefaultConfig(root)

	idx, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	stats, err := idx.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.TotalFiles != 7 {
		t.Errorf("expected 7 files, got %d", stats.TotalFiles)
	}
	if stats.MemoryFootprintKB <= 0 {
		t.Errorf("expected positive memory footprint, got %f", stats.MemoryFootprintKB)
	}
}

func BenchmarkWorkspacePrune(b *testing.B) {
	root := createTestWorkspace(&testing.T{})
	cfg := DefaultConfig(root)
	idx, _ := New(cfg)
	_, _ = idx.Build()

	b.ResetTimer()
	for range b.N {
		_, _ = idx.PruneSearchSpace("ExponentialBackoffRetry", false)
	}
}

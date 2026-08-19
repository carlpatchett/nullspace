package extractor

import (
	"testing"
)

func TestExtractGoTokens(t *testing.T) {
	code := `
package server

import (
	"fmt"
	"net/http"
)

type Config struct {
	MaxRetries int
	TimeoutMs  int
}

func HandleWebhook(w http.ResponseWriter, r *http.Request) {
	endpoint := "/api/v1/webhook"
	fmt.Printf("Received request for %s\n", endpoint)
}
`
	ts := ExtractTokens("src/server/handler.go", []byte(code), DefaultExtractOptions())

	expectedTokens := []string{
		"server",
		"Config",
		"MaxRetries",
		"TimeoutMs",
		"HandleWebhook",
		"endpoint",
		"ResponseWriter",
		"Request",
		"/api/v1/webhook",
		"webhook",
		"handler.go",
		"handler",
		"go",
		"src",
		"server",
		// Subtokens
		"Max",
		"Retries",
		"Timeout",
		"Ms",
		"Handle",
		"Webhook",
		"Response",
		"Writer",
	}

	for _, token := range expectedTokens {
		if _, exists := ts.Tokens[token]; !exists {
			t.Errorf("expected token %q not found in extracted set", token)
		}
	}
}

func TestExtractPythonTokens(t *testing.T) {
	code := `
class ExponentialBackoffRetry:
    """Calculates exponential backoff with jitter for HTTP retries."""

    def __init__(self, base_delay: float = 1.0, max_retries: int = 5):
        self.base_delay = base_delay
        self.max_retries = max_retries
        self.api_url = "https://api.stripe.com/v1/charges"

    def compute_delay(self, attempt: int) -> float:
        import random
        return min(self.base_delay * (2 ** attempt) + random.random(), 60.0)
`
	ts := ExtractTokens("services/payments/retry.py", []byte(code), DefaultExtractOptions())

	expectedTokens := []string{
		"ExponentialBackoffRetry",
		"compute_delay",
		"base_delay",
		"max_retries",
		"api_url",
		"https://api.stripe.com/v1/charges",
		"stripe",
		"charges",
		"retry.py",
		"retry",
		"py",
		"payments",
		"services",
		// Subtokens
		"Exponential",
		"Backoff",
		"Retry",
		"compute",
		"delay",
		"base",
	}

	for _, token := range expectedTokens {
		if _, exists := ts.Tokens[token]; !exists {
			t.Errorf("expected token %q not found in extracted set", token)
		}
	}
}

func TestExtractCSharpTokens(t *testing.T) {
	code := `
namespace LegacyApp.Tools;

public class MailerNotificationService
{
    private readonly ILogger<MailerNotificationService> _logger;
    private const string ApiEndpoint = "https://notify.internal/v2/send";

    public async Task<bool> SendEmailAsync(string recipientEmail, string subject)
    {
        _logger.LogInformation("Sending email to {Recipient}", recipientEmail);
        return await Task.FromResult(true);
    }
}
`
	ts := ExtractTokens("tools/Mailer.cs", []byte(code), DefaultExtractOptions())

	expectedTokens := []string{
		"MailerNotificationService",
		"SendEmailAsync",
		"recipientEmail",
		"ApiEndpoint",
		"https://notify.internal/v2/send",
		"Mailer.cs",
		"Mailer",
		"cs",
		"tools",
		// Subtokens
		"Mailer",
		"Notification",
		"Service",
		"Send",
		"Email",
		"Async",
		"recipient",
	}

	for _, token := range expectedTokens {
		if _, exists := ts.Tokens[token]; !exists {
			t.Errorf("expected token %q not found in extracted set", token)
		}
	}
}

func TestSplitSubtokens(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"camelCaseIdentifier", []string{"camel", "Case", "Identifier"}},
		{"PascalCaseClassName", []string{"Pascal", "Case", "Class", "Name"}},
		{"snake_case_variable_name", []string{"snake", "case", "variable", "name"}},
		{"SCREAMING_SNAKE_CASE", []string{"SCREAMING", "SNAKE", "CASE"}},
		{"kebab-case-component", []string{"kebab", "case", "component"}},
		{"XMLHTTPRequestParser", []string{"XMLHTTP", "Request", "Parser"}},
		{"getHTTP2Client", []string{"get", "HTTP2", "Client"}},
		{"db_connection_pool_v2", []string{"db", "connection", "pool", "v2"}},
		{"singleword", []string{"singleword"}},
		{"", nil},
	}

	for _, tc := range tests {
		got := SplitSubtokens(tc.input)
		if len(got) != len(tc.expected) {
			t.Fatalf("SplitSubtokens(%q) returned %d items, expected %d: %v vs %v",
				tc.input, len(got), len(tc.expected), got, tc.expected)
		}
		for i := range got {
			if got[i] != tc.expected[i] {
				t.Errorf("SplitSubtokens(%q)[%d] = %q, expected %q",
					tc.input, i, got[i], tc.expected[i])
			}
		}
	}
}

func TestIsBinary(t *testing.T) {
	text := []byte("func main() { println(\"Hello world\") }\n")
	if IsBinary(text) {
		t.Errorf("expected text to not be identified as binary")
	}

	binary := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}
	if !IsBinary(binary) {
		t.Errorf("expected ELF header to be identified as binary")
	}
}

func BenchmarkExtractTokens(b *testing.B) {
	code := []byte(`
package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type DatabaseConnectionManager struct {
	pool        *sql.DB
	maxIdleCons int
	timeoutSec  time.Duration
}

func (m *DatabaseConnectionManager) ExecuteTransaction(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	tx, err := m.pool.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return rows, tx.Commit()
}
`)
	opts := DefaultExtractOptions()
	b.ResetTimer()
	for range b.N {
		_ = ExtractTokens("pkg/database/manager.go", code, opts)
	}
}

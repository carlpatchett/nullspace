package extractor

import (
	"path/filepath"
	"strings"
	"unicode"
)

// TokenType categorizes extracted code tokens.
type TokenType int

const (
	TokenIdentifier TokenType = iota
	TokenFunctionCall
	TokenStringLiteral
	TokenFilename
	TokenSubtoken
	TokenKeyword
)

// Token represents an extracted atomic code element.
type Token struct {
	Text string
	Type TokenType
}

// TokenSet collects unique tokens extracted from a file.
type TokenSet struct {
	Tokens map[string]TokenType
}

// NewTokenSet initializes an empty TokenSet.
func NewTokenSet() TokenSet {
	return TokenSet{
		Tokens: make(map[string]TokenType, 128),
	}
}

// Add adds a token to the set, preserving the highest-priority token type.
func (ts *TokenSet) Add(text string, tokenType TokenType) {
	text = strings.TrimSpace(text)
	if len(text) == 0 {
		return
	}
	if _, exists := ts.Tokens[text]; !exists {
		ts.Tokens[text] = tokenType
	}
}

// Len returns the count of unique tokens.
func (ts *TokenSet) Len() int {
	return len(ts.Tokens)
}

// List returns a slice of all unique token strings.
func (ts *TokenSet) List() []string {
	out := make([]string, 0, len(ts.Tokens))
	for t := range ts.Tokens {
		out = append(out, t)
	}
	return out
}

// SplitSubtokens breaks compound identifiers (camelCase, PascalCase, snake_case, kebab-case)
// into individual semantic component words.
// E.g. "ExponentialBackoffRetryStrategy" -> ["Exponential", "Backoff", "Retry", "Strategy"]
// E.g. "db_connection_pool_v2" -> ["db", "connection", "pool", "v2"]
func SplitSubtokens(identifier string) []string {
	if len(identifier) == 0 {
		return nil
	}

	var subtokens []string
	var current strings.Builder
	runes := []rune(identifier)
	n := len(runes)

	for i := range n {
		r := runes[i]

		// Delimiters
		if r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '$' || r == '@' {
			if current.Len() > 0 {
				subtokens = append(subtokens, current.String())
				current.Reset()
			}
			continue
		}

		if unicode.IsUpper(r) {
			// Check if we should split:
			// 1. Prior character was lowercase or digit (e.g. camelCase -> camel | Case)
			// 2. Next character is lowercase and prior was uppercase (e.g. XMLParser -> XML | Parser)
			if current.Len() > 0 {
				priorIsLower := i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]))
				nextIsLower := i+1 < n && unicode.IsLower(runes[i+1]) && i > 0 && unicode.IsUpper(runes[i-1])

				if priorIsLower || nextIsLower {
					subtokens = append(subtokens, current.String())
					current.Reset()
				}
			}
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		subtokens = append(subtokens, current.String())
	}

	return subtokens
}

// ExtractFilenameTokens extracts base names, extensions, and sub-tokens from a file path.
func ExtractFilenameTokens(path string, set *TokenSet) {
	cleanPath := filepath.ToSlash(path)
	base := filepath.Base(cleanPath)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)

	// Full path components
	set.Add(cleanPath, TokenFilename)
	set.Add(strings.ToLower(cleanPath), TokenFilename)
	set.Add(base, TokenFilename)
	set.Add(strings.ToLower(base), TokenFilename)

	if len(ext) > 1 {
		set.Add(ext, TokenFilename)
		set.Add(ext[1:], TokenFilename) // e.g. "go" from ".go"
	}

	if len(nameWithoutExt) > 0 {
		set.Add(nameWithoutExt, TokenFilename)
		set.Add(strings.ToLower(nameWithoutExt), TokenFilename)

		// Subtokens from filename
		for _, sub := range SplitSubtokens(nameWithoutExt) {
			if len(sub) > 1 {
				set.Add(sub, TokenSubtoken)
				set.Add(strings.ToLower(sub), TokenSubtoken)
			}
		}
	}

	// Path segments
	parts := strings.Split(cleanPath, "/")
	for _, part := range parts {
		if len(part) > 0 && part != "." && part != ".." {
			set.Add(part, TokenFilename)
			set.Add(strings.ToLower(part), TokenFilename)
			for _, sub := range SplitSubtokens(part) {
				if len(sub) > 1 {
					set.Add(sub, TokenSubtoken)
					set.Add(strings.ToLower(sub), TokenSubtoken)
				}
			}
		}
	}
}

// Package extractor provides high-throughput lexical token extraction.
package extractor

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ExtractOptions configures token extraction behavior.
type ExtractOptions struct {
	IncludeSubtokens    bool
	IncludeCaseVariants bool
	IncludeStringWords  bool
	MaxStringLiteralLen int
}

// DefaultExtractOptions returns standard extraction configuration.
func DefaultExtractOptions() ExtractOptions {
	return ExtractOptions{
		IncludeSubtokens:    true,
		IncludeCaseVariants: true,
		IncludeStringWords:  true,
		MaxStringLiteralLen: 1024,
	}
}

// IsBinary returns true if the content appears to be binary data (contains null bytes or excessive non-printable chars).
func IsBinary(content []byte) bool {
	checkLen := min(len(content), 1024)
	if checkLen == 0 {
		return false
	}
	nullCount := 0
	nonPrintable := 0
	for i := range checkLen {
		b := content[i]
		if b == 0 {
			nullCount++
		} else if b < 7 || (b > 13 && b < 32) {
			nonPrintable++
		}
	}
	return nullCount > 0 || (float64(nonPrintable)/float64(checkLen)) > 0.30
}

// ExtractTokens extracts all code tokens from a file path and content.
func ExtractTokens(path string, content []byte, opts ExtractOptions) TokenSet {
	set := NewTokenSet()

	// 1. Filename tokens
	if path != "" {
		ExtractFilenameTokens(path, &set)
	}

	if len(content) == 0 || IsBinary(content) {
		return set
	}

	// 2. Lexical scanning over source bytes
	n := len(content)
	i := 0

	for i < n {
		b := content[i]

		// Skip whitespace
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			i++
			continue
		}

		// Line comments: // or # or --
		if b == '/' && i+1 < n && content[i+1] == '/' {
			i += 2
			for i < n && content[i] != '\n' {
				i++
			}
			continue
		}
		if b == '#' {
			i++
			for i < n && content[i] != '\n' {
				i++
			}
			continue
		}
		if b == '-' && i+1 < n && content[i+1] == '-' {
			i += 2
			for i < n && content[i] != '\n' {
				i++
			}
			continue
		}

		// Block comments: /* ... */
		if b == '/' && i+1 < n && content[i+1] == '*' {
			i += 2
			for i+1 < n && !(content[i] == '*' && content[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			}
			continue
		}

		// Python multi-line triple strings/docstrings: """ or '''
		if (b == '"' || b == '\'') && i+2 < n && content[i+1] == b && content[i+2] == b {
			quote := b
			i += 3
			start := i
			for i+2 < n && !(content[i] == quote && content[i+1] == quote && content[i+2] == quote) {
				if content[i] == '\\' {
					i += 2
					continue
				}
				i++
			}
			lit := string(content[start:min(i, n)])
			addStringLiteralTokens(lit, &set, opts)
			if i+2 < n {
				i += 3
			}
			continue
		}

		// String literals: "...", '...', `...`
		if b == '"' || b == '\'' || b == '`' {
			quote := b
			i++
			start := i
			escaped := false
			for i < n {
				if quote != '`' && content[i] == '\\' && !escaped {
					escaped = true
					i++
					continue
				}
				if content[i] == quote && !escaped {
					break
				}
				escaped = false
				i++
			}
			lit := string(content[start:min(i, n)])
			addStringLiteralTokens(lit, &set, opts)
			if i < n && content[i] == quote {
				i++
			}
			continue
		}

		// Identifiers: [a-zA-Z_][a-zA-Z0-9_]* or non-ASCII unicode
		if isIdentStart(b) || b >= utf8.RuneSelf {
			start := i
			for i < n && (isIdentPart(content[i]) || content[i] >= utf8.RuneSelf) {
				i++
			}
			ident := string(content[start:i])

			// Check if this identifier is a function/method call (followed by '(')
			peek := i
			for peek < n && (content[peek] == ' ' || content[peek] == '\t') {
				peek++
			}
			isCall := peek < n && content[peek] == '('

			tokenType := TokenIdentifier
			if isCall {
				tokenType = TokenFunctionCall
			}

			addIdentifierTokens(ident, tokenType, &set, opts)
			continue
		}

		// Other punctuation/operators
		i++
	}

	return set
}

func isIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || b == '$'
}

func isIdentPart(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

func addIdentifierTokens(ident string, tokenType TokenType, set *TokenSet, opts ExtractOptions) {
	if ident == "" {
		return
	}

	set.Add(ident, tokenType)

	if opts.IncludeCaseVariants {
		set.Add(strings.ToLower(ident), tokenType)
	}

	if opts.IncludeSubtokens && len(ident) > 2 {
		subtokens := SplitSubtokens(ident)
		if len(subtokens) > 1 {
			for _, sub := range subtokens {
				if len(sub) > 1 {
					set.Add(sub, TokenSubtoken)
					if opts.IncludeCaseVariants {
						set.Add(strings.ToLower(sub), TokenSubtoken)
					}
				}
			}
		}
	}
}

func addStringLiteralTokens(lit string, set *TokenSet, opts ExtractOptions) {
	lit = strings.TrimSpace(lit)
	if lit == "" {
		return
	}

	if len(lit) <= opts.MaxStringLiteralLen {
		set.Add(lit, TokenStringLiteral)
		if opts.IncludeCaseVariants {
			set.Add(strings.ToLower(lit), TokenStringLiteral)
		}
	}

	if opts.IncludeStringWords {
		// Tokenize words inside the string literal
		words := strings.FieldsFunc(lit, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		})
		for _, w := range words {
			if len(w) > 1 {
				set.Add(w, TokenStringLiteral)
				if opts.IncludeCaseVariants {
					set.Add(strings.ToLower(w), TokenStringLiteral)
				}
				if opts.IncludeSubtokens {
					for _, sub := range SplitSubtokens(w) {
						if len(sub) > 1 {
							set.Add(sub, TokenSubtoken)
							if opts.IncludeCaseVariants {
								set.Add(strings.ToLower(sub), TokenSubtoken)
							}
						}
					}
				}
			}
		}
	}
}

// ExtractTokensFromString convenience wrapper.
func ExtractTokensFromString(path, code string, opts ExtractOptions) TokenSet {
	return ExtractTokens(path, []byte(code), opts)
}

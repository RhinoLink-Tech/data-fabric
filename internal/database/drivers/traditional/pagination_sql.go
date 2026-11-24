package traditional

import (
	"errors"
	"fmt"
	"strings"
)

// paginationStatement removes one final terminator and detects pagination
// keywords outside quoted text and comments. It does not validate read-only
// semantics; that remains the caller's responsibility.
func paginationStatement(query string, mysql bool) (string, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", false, errors.New("batch pagination requires a statement")
	}
	terminator := -1
	hasToken, hasPage, trailingLineComment := false, false, false
	for i := 0; i < len(query); {
		c := query[i]
		if isPaginationSpace(c) {
			i++
			continue
		}
		line := c == '-' && i+1 < len(query) && query[i+1] == '-' && (!mysql || i+2 == len(query) || isPaginationSpace(query[i+2]))
		if mysql && c == '#' {
			line = true
		}
		if line {
			end := strings.IndexAny(query[i:], "\r\n")
			if end < 0 {
				i = len(query)
			} else {
				i += end
			}
			if terminator < 0 {
				trailingLineComment = true
			}
			continue
		}
		if c == '/' && i+1 < len(query) && query[i+1] == '*' {
			// MySQL executable comments can contain SQL and must not be
			// treated as harmless text after a terminator.
			if mysql && (strings.HasPrefix(query[i:], "/*!") || (i+3 < len(query) && (query[i+2] == 'M' || query[i+2] == 'm') && query[i+3] == '!')) {
				return "", false, errors.New("batch pagination does not support executable SQL comments")
			}
			depth := 1
			i += 2
			for i < len(query) && depth > 0 {
				if !mysql && i+1 < len(query) && query[i:i+2] == "/*" {
					depth++
					i += 2
				} else if i+1 < len(query) && query[i:i+2] == "*/" {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				return "", false, errors.New("unterminated SQL comment")
			}
			if terminator < 0 {
				trailingLineComment = false
			}
			continue
		}
		if terminator >= 0 {
			return "", false, errors.New("batch pagination accepts a single statement")
		}
		if c == ';' {
			terminator = i
			i++
			continue
		}
		trailingLineComment = false
		hasToken = true
		if c == '\'' || c == '"' || (mysql && c == 0x60) {
			quote := c
			i++
			closed := false
			for i < len(query) {
				if query[i] == '\\' && i+1 < len(query) && (query[i+1] == quote || query[i+1] == '\\') {
					// Escape behavior depends on server SQL modes. Refuse
					// ambiguous quoting rather than misclassify a statement.
					return "", false, errors.New("ambiguous SQL escape in batch pagination")
				}
				if query[i] == quote {
					if i+1 < len(query) && query[i+1] == quote {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return "", false, errors.New("unterminated SQL quote")
			}
			continue
		}
		if !mysql && c == '$' {
			end := i + 1
			for end < len(query) && (query[end] == '_' || query[end] >= 'a' && query[end] <= 'z' || query[end] >= 'A' && query[end] <= 'Z' || end > i+1 && query[end] >= '0' && query[end] <= '9') {
				end++
			}
			if end < len(query) && query[end] == '$' {
				delimiter := query[i : end+1]
				closeAt := strings.Index(query[end+1:], delimiter)
				if closeAt < 0 {
					return "", false, errors.New("unterminated SQL dollar quote")
				}
				i = end + 1 + closeAt + len(delimiter)
				continue
			}
		}
		if isPaginationIdentStart(c) {
			start := i
			i++
			for i < len(query) && isPaginationIdent(query[i]) {
				i++
			}
			word := query[start:i]
			if strings.EqualFold(word, "LIMIT") || strings.EqualFold(word, "OFFSET") {
				hasPage = true
			}
			continue
		}
		i++
	}
	if !hasToken {
		return "", false, errors.New("batch pagination requires a statement")
	}
	if terminator >= 0 {
		query = query[:terminator]
	}
	query = strings.TrimSpace(query)
	if trailingLineComment {
		query += "\n"
	}
	return query, hasPage, nil
}

func isPaginationSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v'
}
func isPaginationIdentStart(c byte) bool {
	return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}
func isPaginationIdent(c byte) bool {
	return isPaginationIdentStart(c) || c >= '0' && c <= '9' || c == '$'
}

func paginateLimitOffset(query string, batchSize, offset int64, mysql bool) (string, error) {
	normalized, paginated, err := paginationStatement(query, mysql)
	if err != nil {
		return "", err
	}
	if paginated {
		return fmt.Sprintf("SELECT * FROM (%s) AS batch_query LIMIT %d OFFSET %d", normalized, batchSize, offset), nil
	}
	if offset > 0 {
		return fmt.Sprintf("%s LIMIT %d OFFSET %d", normalized, batchSize, offset), nil
	}
	return fmt.Sprintf("%s LIMIT %d", normalized, batchSize), nil
}

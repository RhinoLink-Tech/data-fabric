package domestic

import (
	"errors"
	"strings"
	"unicode"
)

// errMultipleStatements is returned when a query handed to batch pagination
// holds more than one statement.
var errMultipleStatements = errors.New("batch pagination accepts a single statement")

// trimStatementTerminator removes one trailing statement terminator from a
// single statement so that pagination clauses can be appended to it or the
// statement can be wrapped in a subquery.
//
// Semicolons inside string literals, quoted identifiers and comments are not
// terminators. A terminator followed by anything other than whitespace or
// comments means the input holds more than one statement, which is refused.
func trimStatementTerminator(query string) (string, error) {
	query = strings.TrimSpace(query)
	terminator := -1
	lastLineStart, lastLineEnd := -1, -1
	for i := 0; i < len(query); i++ {
		c := query[i]
		lineComment := c == '-' && i+1 < len(query) && query[i+1] == '-'
		blockComment := c == '/' && i+1 < len(query) && query[i+1] == '*'
		// After the terminator, only whitespace and comments may follow.
		// Check before skipping quotes so trailing quoted tokens are rejected.
		if terminator >= 0 && !isSQLSpace(c) && !lineComment && !blockComment {
			return "", errMultipleStatements
		}
		switch {
		case c == '\'' || c == '"':
			closed := false
			for i++; i < len(query); i++ {
				if query[i] == c {
					if i+1 < len(query) && query[i+1] == c {
						i++
						continue
					}
					closed = true
					break
				}
			}
			if !closed {
				return "", errors.New("unterminated SQL quote")
			}
		case lineComment:
			end := strings.IndexAny(query[i:], "\r\n")
			if terminator < 0 {
				lastLineStart = i
				lastLineEnd = len(query)
				if end >= 0 {
					lastLineEnd = i + end
				}
			}
			if end < 0 {
				i = len(query)
			} else {
				i += end
			}
		case blockComment:
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return "", errors.New("unterminated SQL block comment")
			}
			i += end + 3
		case c == ';':
			terminator = i
		}
	}
	end := len(query)
	if terminator >= 0 {
		end = terminator
	}
	// Preserve a line boundary when the trimmed SQL ends in a -- comment,
	// so generated pagination or the closing parenthesis is not commented out.
	prefix := strings.TrimSpace(query[:end])
	trimmedEnd := len(strings.TrimRightFunc(query[:end], unicode.IsSpace))
	if lastLineStart >= 0 && lastLineStart < trimmedEnd && trimmedEnd <= lastLineEnd {
		prefix += "\n"
	}
	return prefix, nil
}

func isSQLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v'
}

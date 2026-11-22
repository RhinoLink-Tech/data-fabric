package domestic

import (
	"errors"
	"strings"
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
	for i := 0; i < len(query); i++ {
		switch c := query[i]; {
		case c == '\'' || c == '"':
			// Skip a quoted literal or identifier; a doubled quote is an
			// escaped quote and keeps the literal open.
			for i++; i < len(query); i++ {
				if query[i] == c {
					if i+1 < len(query) && query[i+1] == c {
						i++
						continue
					}
					break
				}
			}
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				i = len(query)
			} else {
				i += end
			}
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				i = len(query)
			} else {
				i += end + 3
			}
		case c == ';':
			if terminator >= 0 {
				return "", errMultipleStatements
			}
			terminator = i
		case terminator >= 0 && !isSQLSpace(c):
			return "", errMultipleStatements
		}
	}
	if terminator < 0 {
		return query, nil
	}
	return strings.TrimSpace(query[:terminator]), nil
}

func isSQLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v'
}

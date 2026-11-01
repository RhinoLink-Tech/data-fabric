package execution

import (
	"context"
	"fmt"
	"strconv"
	"sync"
)

// SliceStream adapts a materialized result to the common bounded-batch contract.
type SliceStream struct {
	mu       sync.Mutex
	columns  []Column
	rows     [][]any
	metadata ResultMetadata
	offset   int
	closed   bool
}

func NewSliceStream(columns []Column, rows [][]any, metadata ResultMetadata) (*SliceStream, error) {
	cc := append([]Column(nil), columns...)
	cr := make([][]any, len(rows))
	for i, row := range rows {
		if len(row) != len(cc) {
			return nil, newError(ErrorDecode, "create result stream", fmt.Errorf("row %d has %d values, want %d", i, len(row), len(cc)))
		}
		cr[i] = cloneRow(row)
	}
	return &SliceStream{columns: cc, rows: cr, metadata: metadata}, nil
}
func (s *SliceStream) Columns() []Column {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Column(nil), s.columns...)
}
func (s *SliceStream) Metadata() ResultMetadata { s.mu.Lock(); defer s.mu.Unlock(); return s.metadata }
func (s *SliceStream) NextBatch(ctx context.Context, maxRows int) (Batch, error) {
	if ctx == nil {
		return Batch{}, newError(ErrorInvalidRequest, "read result batch", fmt.Errorf("context is required"))
	}
	if maxRows < 1 {
		return Batch{}, newError(ErrorInvalidRequest, "read result batch", fmt.Errorf("maxRows must be positive"))
	}
	if err := ctx.Err(); err != nil {
		return Batch{}, WrapError("read result batch", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Batch{}, newError(ErrorClosed, "read result batch", fmt.Errorf("result stream is closed"))
	}
	count := maxRows
	if remaining := len(s.rows) - s.offset; count > remaining {
		count = remaining
	}
	end := s.offset + count
	batchRows := make([][]any, end-s.offset)
	for i, row := range s.rows[s.offset:end] {
		batchRows[i] = cloneRow(row)
	}
	s.offset = end
	more := s.offset < len(s.rows)
	cursor := ""
	if more {
		cursor = strconv.Itoa(s.offset)
	}
	return Batch{Rows: batchRows, Cursor: cursor, HasMore: more}, nil
}
func (s *SliceStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.rows = nil
	return nil
}
func cloneRow(row []any) []any {
	copied := append([]any(nil), row...)
	for i, v := range copied {
		if b, ok := v.([]byte); ok {
			copied[i] = append([]byte(nil), b...)
		}
	}
	return copied
}

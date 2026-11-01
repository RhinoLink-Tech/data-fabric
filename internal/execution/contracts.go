package execution

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Mode identifies how an executor reaches its source.
type Mode string

const (
	ModeSQL       Mode = "sql"
	ModeObject    Mode = "object_scan"
	ModeNativeAPI Mode = "native_api"
)

// Capabilities describes the operations and guarantees available from an executor.
type Capabilities struct {
	Mode                        Mode
	SupportsParameters          bool
	SupportsSchemaDiscovery     bool
	SupportsBatching            bool
	SupportsCancellation        bool
	SupportsServerSideFiltering bool
}

// Column describes one stable result column. NullableKnown distinguishes an
// unknown nullability from a known non-nullable column.
type Column struct {
	Name          string
	Type          string
	Nullable      bool
	NullableKnown bool
}

// Parameter is a typed value bound to a source query.
type Parameter struct {
	Type  string
	Value any
}

// ResultMetadata carries source execution metadata without exposing a
// connector-specific response shape.
type ResultMetadata struct {
	QueryID   string
	TotalRows int64 // -1 means the source cannot provide an exact count.
	Duration  time.Duration
}

// Batch is one bounded portion of a result. Cursor is opaque to the gateway;
// executors that hold their own stream may leave it empty.
type Batch struct {
	Rows    [][]any
	Cursor  string
	HasMore bool
}

// ResultStream exposes typed columns and bounded, cancellable result batches.
type ResultStream interface {
	Columns() []Column
	Metadata() ResultMetadata
	NextBatch(ctx context.Context, maxRows int) (Batch, error)
	Close() error
}

// SQLRequest describes a parameterized SQL operation.
type SQLRequest struct {
	Query      string
	Parameters []any
}

// Predicate is a source-neutral filter for object or file scans.
type Predicate struct {
	Column   string
	Operator string
	Value    any
}

// ObjectScanRequest describes a bounded scan over one object or file.
type ObjectScanRequest struct {
	URI        string
	Format     string
	Columns    []string
	Predicates []Predicate
	Limit      int64
	Offset     int64
	BatchSize  int
}

// NativeAPIRequest describes an operation performed through a source's
// native HTTP, RPC, or SDK API.
type NativeAPIRequest struct {
	Operation  string
	Query      string
	Parameters []Parameter
	Payload    []byte
	BatchSize  int
	Limit      int
	Offset     int64
}

// SQLExecutor handles SQL-capable sources without prescribing their protocol.
type SQLExecutor interface {
	Capabilities() Capabilities
	ExecuteSQL(ctx context.Context, request SQLRequest) (ResultStream, error)
}

// ObjectScanExecutor handles object and file scans without requiring a
// database/sql driver.
type ObjectScanExecutor interface {
	Capabilities() Capabilities
	ScanObject(ctx context.Context, request ObjectScanRequest) (ResultStream, error)
}

// NativeAPIExecutor handles sources whose primary interface is a native API.
type NativeAPIExecutor interface {
	Capabilities() Capabilities
	ExecuteNativeAPI(ctx context.Context, request NativeAPIRequest) (ResultStream, error)
}

// ErrorKind classifies execution failures independently of the source protocol.
type ErrorKind string

const (
	ErrorInvalidRequest ErrorKind = "invalid_request"
	ErrorUnsupported    ErrorKind = "unsupported"
	ErrorCancelled      ErrorKind = "cancelled"
	ErrorTimeout        ErrorKind = "timeout"
	ErrorRemote         ErrorKind = "remote"
	ErrorDecode         ErrorKind = "decode"
	ErrorClosed         ErrorKind = "closed"
)

// Error preserves a stable error category while allowing callers to inspect
// the underlying source or context error with errors.Is and errors.As.
type Error struct {
	Kind      ErrorKind
	Operation string
	Err       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Operation == "" {
		return fmt.Sprintf("execution %s: %v", e.Kind, e.Err)
	}
	return fmt.Sprintf("execution %s during %s: %v", e.Kind, e.Operation, e.Err)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// WrapError classifies context cancellation and timeout consistently.
func WrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	kind := ErrorRemote
	switch {
	case errors.Is(err, context.Canceled):
		kind = ErrorCancelled
	case errors.Is(err, context.DeadlineExceeded):
		kind = ErrorTimeout
	}
	return &Error{Kind: kind, Operation: operation, Err: err}
}

func newError(kind ErrorKind, operation string, err error) error {
	return &Error{Kind: kind, Operation: operation, Err: err}
}

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestSafeQueryErrorMessageHidesInternalDetails(t *testing.T) {
	err := errors.New("coordinator at internal.host returned password=do-not-leak and token=also-secret")
	message := SafeQueryErrorMessage(err)
	for _, secret := range []string{"internal.host", "do-not-leak", "also-secret"} {
		if strings.Contains(message, secret) {
			t.Errorf("public error leaked %q: %s", secret, message)
		}
	}
	if message == "" || message == err.Error() {
		t.Fatalf("expected a safe actionable message, got %q", message)
	}
	if got := SafeQueryErrorMessage(security.ErrNotSelectQuery); got != "Only read-only SELECT queries are supported." {
		t.Fatalf("read-only query error = %q", got)
	}
	if got := SafeQueryErrorMessage(security.ErrSQLSyntaxError); got != "The query is invalid or uses unsupported SQL syntax." {
		t.Fatalf("syntax error = %q", got)
	}
}

func TestLegacyQueryErrorResponseOmitsSQLAndDriverDetails(t *testing.T) {
	query := "SELECT 'password=sql-secret' FROM private.customers"
	internalErr := errors.New("coordinator at db.internal leaked password=driver-secret")
	response := (&queryService{}).createErrorResponse(internalErr, query)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, secret := range []string{"sql-secret", "private.customers", "db.internal", "driver-secret", query} {
		if strings.Contains(body, secret) {
			t.Errorf("legacy query response leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, "The data-source query failed") {
		t.Errorf("response lacks the safe query message: %s", body)
	}
}

// A cancelled PostgreSQL statement surfaces as the server's own 57014 error,
// not context.Canceled; it must still map to the stable cancellation message
// instead of the generic data-source failure text (#261).
func TestSafeQueryErrorMessageClassifiesServerSideCancellation(t *testing.T) {
	cancelled := &pq.Error{Code: "57014", Message: "canceling statement due to user request"}
	if got := SafeQueryErrorMessage(cancelled); got != "The query was cancelled before completion." {
		t.Fatalf("cancelled statement message = %q", got)
	}
	wrapped := fmt.Errorf("execute query: %w", cancelled)
	if got := SafeQueryErrorMessage(wrapped); got != "The query was cancelled before completion." {
		t.Fatalf("wrapped cancelled statement message = %q", got)
	}
	other := &pq.Error{Code: "42601", Message: "syntax error at or near \"FROM\""}
	if got := SafeQueryErrorMessage(other); got != "The data-source query failed. Check the SQL dialect, catalog and schema, permissions, and query-engine availability." {
		t.Fatalf("other server error message = %q", got)
	}
}

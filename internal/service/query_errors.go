package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

var (
	ErrQuerySessionUnavailable = errors.New("query session is unavailable or expired")
	ErrInvalidQuerySession     = errors.New("query session credentials are invalid")
	// ErrDataSourceInactive rejects query execution against a data source whose
	// status is not active (#118). Anything but active — inactive, error, or an
	// unexpected empty status — fails closed.
	ErrDataSourceInactive = errors.New("data source is not active")
)

// requireActiveDataSource gates query execution on the data source being
// active (#118). Callers have already resolved the source by ID, so existence
// is settled and only the status gap remains.
func requireActiveDataSource(dataSource *model.DataSource) error {
	if dataSource.Status != model.DataSourceStatusActive {
		return fmt.Errorf("%w: %s has status %q", ErrDataSourceInactive, dataSource.ID, dataSource.Status)
	}
	return nil
}

// SafeQueryErrorMessage maps internal failures to stable, user-actionable text.
func SafeQueryErrorMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "The query timed out. Reduce the query scope or increase the request timeout."
	case errors.Is(err, context.Canceled):
		return "The query was cancelled before completion."
	case errors.Is(err, repository.ErrDataSourceNotFound):
		return "The selected data source was not found."
	case errors.Is(err, security.ErrDataSourceAccessDenied):
		return "You do not have access to this data source"
	case errors.Is(err, ErrDataSourceInactive):
		return "The selected data source is not active. Ask an administrator to re-enable it, then retry the query."
	case errors.Is(err, ErrQuerySessionUnavailable), errors.Is(err, ErrInvalidQuerySession):
		return "The query session is invalid or has expired. Submit the query again."
	case errors.Is(err, security.ErrNotSelectQuery):
		return "Only read-only SELECT queries are supported."
	case errors.Is(err, security.ErrSQLSyntaxError), errors.Is(err, security.ErrDangerousKeyword),
		errors.Is(err, security.ErrEmptyQuery), errors.Is(err, security.ErrQueryTooLong):
		return "The query is invalid or uses unsupported SQL syntax."
	case errors.Is(err, security.ErrSQLInjection):
		return "The query did not pass SQL safety validation."
	default:
		return "The data-source query failed. Check the SQL dialect, catalog and schema, permissions, and query-engine availability."
	}
}

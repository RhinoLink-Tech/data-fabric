package service

import (
	"context"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

// reauthorizeSavedQuery keeps a cursor or paginated result aligned with current
// policy. Table-only grants can be checked from the saved references; a column
// allowlist requires reparsing the exact SQL that created the session.
func reauthorizeSavedQuery(ctx context.Context, caller *security.Caller, dataSource *model.DataSource, authorizer *security.TableAuthorizer, validator *security.SQLValidator, query string, tables []sqlanalysis.TableReference) error {
	if authorizer == nil || !authorizer.Enabled() {
		return nil
	}
	if err := authorizer.AuthorizeReferences(ctx, caller, dataSource, tables, security.TablePermissionRead); err != nil {
		return err
	}
	if !authorizer.RequiresColumnAuthorization(caller, dataSource, tables, security.TablePermissionRead) {
		return nil
	}
	if validator == nil || !validator.SupportsASTAnalysis() {
		return security.ErrTableAuthorizationUnavailable
	}
	analysis, err := validator.AnalyzeStatementForDataSourceContext(ctx, query, dataSource.Type)
	if err != nil {
		return err
	}
	return authorizer.AuthorizeAnalysis(ctx, caller, dataSource, analysis, security.TablePermissionRead)
}

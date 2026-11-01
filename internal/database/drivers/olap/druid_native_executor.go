package olap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/execution"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenNativeAPIExecutor creates a native HTTP executor for Druid. The SQL
// language remains Druid SQL; this path does not emulate a database/sql wire protocol.
func (d *DruidDriver) OpenNativeAPIExecutor(ctx context.Context, config *model.DataSourceConfig) (execution.NativeAPIExecutor, error) {
	if ctx == nil {
		return nil, &execution.Error{Kind: execution.ErrorInvalidRequest, Operation: "open Druid executor", Err: fmt.Errorf("context is required")}
	}
	if err := ctx.Err(); err != nil {
		return nil, execution.WrapError("open Druid executor", err)
	}
	endpoint, err := druidEndpointFromConfig(config)
	if err != nil {
		return nil, &execution.Error{Kind: execution.ErrorInvalidRequest, Operation: "open Druid executor", Err: err}
	}
	client := NewDruidRESTClient(endpoint)
	client.SetBasicAuth(config.Username, config.Password)
	return &druidNativeAPIExecutor{client: client}, nil
}

type druidNativeAPIExecutor struct{ client *DruidRESTClient }

func (e *druidNativeAPIExecutor) Capabilities() execution.Capabilities {
	return execution.Capabilities{Mode: execution.ModeNativeAPI, SupportsParameters: true, SupportsSchemaDiscovery: true, SupportsBatching: true, SupportsCancellation: true, SupportsServerSideFiltering: true}
}
func (e *druidNativeAPIExecutor) ExecuteNativeAPI(ctx context.Context, request execution.NativeAPIRequest) (execution.ResultStream, error) {
	if ctx == nil {
		return nil, &execution.Error{Kind: execution.ErrorInvalidRequest, Operation: "execute Druid API", Err: fmt.Errorf("context is required")}
	}
	if e == nil || e.client == nil {
		return nil, &execution.Error{Kind: execution.ErrorInvalidRequest, Operation: "execute Druid API", Err: fmt.Errorf("Druid REST client is required")}
	}
	if !strings.EqualFold(strings.TrimSpace(request.Operation), "sql") {
		return nil, &execution.Error{Kind: execution.ErrorUnsupported, Operation: "execute Druid API", Err: fmt.Errorf("operation %q is not supported", request.Operation)}
	}
	if request.Limit < 0 || request.Offset < 0 || (request.Offset > 0 && request.Limit == 0) {
		return nil, &execution.Error{Kind: execution.ErrorInvalidRequest, Operation: "paginate Druid SQL API query", Err: fmt.Errorf("limit and offset must be non-negative and offset requires a positive limit")}
	}
	if !isDruidReadOnlyQuery(request.Query) {
		return nil, &execution.Error{Kind: execution.ErrorUnsupported, Operation: "execute Druid SQL API", Err: errDruidReadOnly}
	}
	if err := ctx.Err(); err != nil {
		return nil, execution.WrapError("execute Druid SQL API", err)
	}
	query := request.Query
	if request.Limit > 0 {
		paginatedQuery, err := (&DruidDriver{}).ApplyBatchPagination(query, int64(request.Limit), request.Offset)
		if err != nil {
			return nil, &execution.Error{Kind: execution.ErrorInvalidRequest, Operation: "paginate Druid SQL API query", Err: err}
		}
		query = paginatedQuery
	}

	params := make([]DruidQueryParameter, 0, len(request.Parameters))
	for i, p := range request.Parameters {
		converted, err := druidParameter(p.Value)
		if err != nil {
			return nil, &execution.Error{Kind: execution.ErrorInvalidRequest, Operation: "bind Druid SQL parameter", Err: fmt.Errorf("parameter %d: %w", i+1, err)}
		}
		if strings.TrimSpace(p.Type) != "" {
			converted.Type = strings.ToUpper(strings.TrimSpace(p.Type))
		}
		params = append(params, converted)
	}
	started := time.Now()
	result, err := e.client.ExecuteSQLQueryWithParameters(ctx, query, params)
	if err != nil {
		return nil, execution.WrapError("execute Druid SQL API", err)
	}
	columns := make([]execution.Column, len(result.Columns))
	for i, name := range result.Columns {
		typeName := "VARCHAR"
		if i < len(result.ColumnTypes) && strings.TrimSpace(result.ColumnTypes[i]) != "" {
			typeName = result.ColumnTypes[i]
		}
		columns[i] = execution.Column{Name: name, Type: typeName}
	}
	rows := make([][]any, len(result.Values))
	for i, row := range result.Values {
		rows[i] = make([]any, len(row))
		for j, value := range row {
			rows[i][j] = toDruidDriverValue(value)
		}
	}
	return execution.NewSliceStream(columns, rows, execution.ResultMetadata{QueryID: result.QueryID, TotalRows: int64(result.RowCount), Duration: time.Since(started)})
}

func (d *DruidDriver) CheckNativeAPIConnection(ctx context.Context, config *model.DataSourceConfig) error {
	executor, err := d.OpenNativeAPIExecutor(ctx, config)
	if err != nil {
		return err
	}
	stream, err := executor.ExecuteNativeAPI(ctx, execution.NativeAPIRequest{Operation: "sql", Query: "SELECT 1", Limit: 1, BatchSize: 1})
	if err != nil {
		return err
	}
	if stream == nil {
		return fmt.Errorf("Druid executor returned a nil health result stream")
	}
	defer stream.Close()
	batch, err := stream.NextBatch(ctx, 1)
	if err != nil {
		return err
	}
	if len(batch.Rows) != 1 || len(batch.Rows[0]) != 1 {
		return fmt.Errorf("Druid health query returned an unexpected result shape")
	}
	return nil
}

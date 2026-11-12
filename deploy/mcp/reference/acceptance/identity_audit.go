package acceptance

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type sourceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Config   *struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Database string `json:"database"`
	} `json:"config"`
}

func decodeSources(w wire, result reply) ([]sourceInfo, error) {
	var response struct {
		REST       []sourceInfo `json:"dataSources"`
		MCPSources []sourceInfo `json:"data_sources"`
		Total      int          `json:"total"`
		Count      int          `json:"count"`
	}
	if err := result.decode(&response); err != nil {
		return nil, err
	}
	list, count := response.REST, response.Total
	if w.kind == mcpHTTP {
		list, count = response.MCPSources, response.Count
	}
	if len(list) != count {
		return nil, fmt.Errorf("source count differs from visible list")
	}
	return list, nil
}

func discoverSources(ctx context.Context, w wire) error {
	for _, actor := range []string{"app-a", reader, outsider} {
		r, err := w.listSources(ctx, actor)
		if err != nil {
			return err
		}
		if err := safePayload(r, bearerValues(w.env)...); err != nil {
			return err
		}
		list, err := decodeSources(w, r)
		if err != nil {
			return err
		}
		if actor == outsider {
			if len(list) != 0 {
				return fmt.Errorf("another tenant discovered a source")
			}
			continue
		}
		expectedSources := len(w.env.Sources) + len(w.env.TenantBSources)
		if len(list) != expectedSources {
			return fmt.Errorf("expected %d configured sources for the owner and tenant reader, got %d", expectedSources, len(list))
		}
		for key, source := range w.env.Sources {
			if source.ID == "" || !slices.ContainsFunc(list, func(info sourceInfo) bool {
				return info.ID == source.ID && info.Type == source.Type &&
					info.Name == "reference-"+key && info.Status == "active"
			}) {
				return fmt.Errorf("missing or unstable %s identity", key)
			}
		}
		for key, source := range w.env.TenantBSources {
			if source.ID == "" || !slices.ContainsFunc(list, func(info sourceInfo) bool {
				return info.ID == source.ID && info.Type == source.Type &&
					info.Name == "reference-"+key+"-tenant-b" && info.Status == "active"
			}) {
				return fmt.Errorf("missing or unstable tenant-b %s identity", key)
			}
		}
	}
	return nil
}

func inspectSources(ctx context.Context, w wire) error {
	return forEachSource(w, func(key string, source reference.Source) error {
		r, err := w.getSource(ctx, reader, source)
		if err != nil {
			return err
		}
		var info sourceInfo
		if err := r.decode(&info); err != nil {
			return err
		}
		if info.ID != source.ID || info.Name != "reference-"+key ||
			info.Type != source.Type || info.Status != "active" {
			return fmt.Errorf("source detail changed identity")
		}
		host, port, database := info.Host, info.Port, info.Database
		if w.kind == rest && info.Config != nil {
			host, port, database = info.Config.Host, info.Config.Port, info.Config.Database
		}
		if host == "" || port <= 0 || database == "" {
			return fmt.Errorf("source detail omitted host, port or database")
		}
		return safePayload(r, bearerValues(w.env)...)
	})
}

func listTables(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		r, err := w.tool(ctx, reader, "", "list_tables", map[string]any{"datasource_id": source.ID})
		page, err := readBatch(r, err)
		if err != nil {
			return err
		}
		if len(page.Entries) != 2 || page.TotalCount != 2 {
			return fmt.Errorf("table list differs from the two permitted tables")
		}
		var names []string
		for _, row := range page.Entries {
			if len(row) != 1 {
				return fmt.Errorf("unexpected table row")
			}
			name, ok := row[0].(string)
			if !ok {
				return fmt.Errorf("table name is not a string")
			}
			names = append(names, name)
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{"order_items", "orders"}) {
			return fmt.Errorf("table list differs from the two permitted tables")
		}
		return nil
	})
}

func tableDetail(ctx context.Context, w wire, source reference.Source, table string) (reply, error) {
	return w.tool(ctx, reader, "", "describe_table", map[string]any{
		"datasource_id": source.ID, "table_name": table, "schema": source.Schema,
	})
}

func describeTable(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		r, err := tableDetail(ctx, w, source, "orders")
		if err != nil {
			return err
		}
		var detail struct {
			SourceID string `json:"datasource_id"`
			Table    struct {
				Name       string   `json:"name"`
				Schema     string   `json:"schema"`
				Columns    []column `json:"columns"`
				PrimaryKey []string `json:"primaryKey"`
			} `json:"table"`
		}
		if err := r.decode(&detail); err != nil {
			return err
		}
		if detail.SourceID != source.ID || detail.Table.Name != "orders" || detail.Table.Schema != source.Schema ||
			!matchesColumns(detail.Table.Columns, "amount", "created_at", "id", "status") ||
			!slices.Equal(detail.Table.PrimaryKey, []string{"id"}) {
			return fmt.Errorf("visible orders schema differs from the fixture")
		}
		return safePayload(r, "customer", "Ada Lovelace", "Grace Hopper", "Alan Turing")
	})
}

func searchSchema(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		type result struct {
			DataSourceID string `json:"datasource_id"`
			Query        string `json:"query"`
			Matches      []struct {
				Schema        string   `json:"schema"`
				Table         string   `json:"table"`
				MatchedFields []string `json:"matched_fields"`
				Columns       []string `json:"matched_columns"`
			} `json:"matches"`
			Ambiguous bool `json:"ambiguous"`
			Truncated bool `json:"truncated"`
		}
		search := func(query string, limit int) (reply, result, error) {
			r, err := w.tool(ctx, reader, "", "search_schema", map[string]any{
				"datasource_id": source.ID, "query": query, "schema": source.Schema, "limit": float64(limit),
			})
			var response result
			if err == nil {
				err = r.decode(&response)
			}
			return r, response, err
		}

		r, amount, err := search("amount", 5)
		if err != nil {
			return err
		}
		if amount.DataSourceID != source.ID || amount.Query != "amount" || len(amount.Matches) != 1 ||
			amount.Ambiguous || amount.Truncated || amount.Matches[0].Schema != source.Schema ||
			amount.Matches[0].Table != "orders" || !slices.Equal(amount.Matches[0].MatchedFields, []string{"column_name"}) ||
			!slices.Equal(amount.Matches[0].Columns, []string{"amount"}) {
			return fmt.Errorf("search result for the permitted amount column is unstable or incomplete")
		}

		_, hidden, err := search("customer", 5)
		if err != nil {
			return err
		}
		if len(hidden.Matches) != 0 {
			return fmt.Errorf("search returned a column hidden by metadata policy")
		}

		r, ambiguous, err := search("id", 1)
		if err != nil {
			return err
		}
		if len(ambiguous.Matches) != 1 || ambiguous.Matches[0].Table != "order_items" ||
			!slices.Contains(ambiguous.Matches[0].Columns, "id") ||
			!ambiguous.Ambiguous || !ambiguous.Truncated {
			return fmt.Errorf("limited duplicate-name search did not report ambiguity and truncation")
		}
		return safePayload(r, "customer", "Ada Lovelace", "Grace Hopper", "Alan Turing")
	})
}

func invalidTokens(ctx context.Context, w wire) error {
	mint := w.env.MintREST
	valid := w.env.Identities[reader].RESTToken
	if w.kind == mcpHTTP {
		mint = w.env.MintMCP
		valid = w.env.Identities[reader].MCPToken
	}
	expired, err := mint(reader, func(claims jwt.MapClaims) {
		claims["exp"] = time.Now().Add(-5 * time.Minute).Unix()
	})
	if err != nil {
		return fmt.Errorf("mint expired fixture bearer")
	}
	pos := strings.LastIndexByte(valid, '.') + 1
	if pos <= 0 || pos >= len(valid) {
		return fmt.Errorf("invalid fixture token shape")
	}
	replacement := "A"
	if valid[pos] == 'A' {
		replacement = "B"
	}
	invalid := valid[:pos] + replacement + valid[pos+1:]
	if err := w.rejectAuth(ctx, expired); err != nil {
		return fmt.Errorf("expired bearer: %w", err)
	}
	if err := w.rejectAuth(ctx, invalid); err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}
	return w.env.AssertLogsOmit(ctx, expired, invalid, valid)
}

func rejectedClaim(ctx context.Context, w wire, mutate func(jwt.MapClaims)) error {
	token, err := w.env.MintMCP(reader, mutate)
	if err != nil {
		return fmt.Errorf("mint OIDC fixture bearer")
	}
	return w.rejectAuth(ctx, token)
}

func wrongIssuer(ctx context.Context, w wire) error {
	return rejectedClaim(ctx, w, func(claims jwt.MapClaims) {
		claims["iss"] = "https://wrong-issuer.invalid"
	})
}

func wrongAudience(ctx context.Context, w wire) error {
	return rejectedClaim(ctx, w, func(claims jwt.MapClaims) {
		claims["aud"] = "wrong-audience"
	})
}

func missingScope(ctx context.Context, w wire) error {
	return rejectedClaim(ctx, w, func(claims jwt.MapClaims) {
		delete(claims, "scope")
	})
}

func crossTenant(ctx context.Context, w wire) error {
	r, err := w.listSources(ctx, outsider)
	if err != nil {
		return err
	}
	list, err := decodeSources(w, r)
	if err != nil {
		return err
	}
	if len(list) != 0 {
		return fmt.Errorf("another tenant discovered source metadata")
	}
	return forEachSource(w, func(_ string, source reference.Source) error {
		metadata, err := w.getSource(ctx, outsider, source)
		if err != nil {
			return err
		}
		hidden := sourceDenied
		if w.kind == mcpHTTP {
			hidden = sourceHidden
		}
		if err := metadata.expectError(w.kind, http.StatusForbidden, "DATASOURCE_ACCESS_DENIED", hidden); err != nil {
			return fmt.Errorf("source metadata: %w", err)
		}
		query, err := w.query(ctx, outsider, "", source, orderedIDs, nil, 10, 5)
		if err != nil {
			return err
		}
		return query.expectError(w.kind, http.StatusForbidden, "DATASOURCE_ACCESS_DENIED", sourceDenied)
	})
}

func deniedTable(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		if err := deniedQuery(ctx, w, reader, source, "SELECT id FROM private_orders"); err != nil {
			return err
		}
		if w.kind == rest {
			return nil
		}
		r, err := tableDetail(ctx, w, source, "private_orders")
		if err != nil {
			return err
		}
		return r.expectError(w.kind, http.StatusForbidden, "TABLE_ACCESS_DENIED", tableHidden)
	})
}

type auditCheck struct {
	actor    string
	sql      string
	decision string
	outcome  string
	status   int
	code     string
	message  string
	rows     int
}

func auditEvidence(ctx context.Context, w wire) error {
	const successSQL = "SELECT amount FROM orders ORDER BY id"
	source := w.env.Sources["postgresql"]
	tests := []auditCheck{
		{actor: reader, sql: successSQL, decision: "allow", outcome: "success", status: http.StatusOK, rows: 1},
		{actor: outsider, sql: orderedIDs, decision: "deny", outcome: "denied", status: http.StatusForbidden, code: "DATASOURCE_ACCESS_DENIED", message: sourceDenied},
		{actor: reader, sql: malformed, decision: "error", outcome: "failed", status: http.StatusBadRequest, code: "INVALID_QUERY", message: invalidQuery},
	}
	var failures []error
	for _, check := range tests {
		if err := auditQuery(ctx, w, source, check); err != nil {
			failures = append(failures, fmt.Errorf("%s audit: %w", check.outcome, err))
		}
	}
	return errors.Join(failures...)
}

func auditQuery(ctx context.Context, w wire, source reference.Source, check auditCheck) error {
	correlation := uuid.NewString()
	r, err := w.query(ctx, check.actor, correlation, source, check.sql, nil, 1, 5)
	if err != nil {
		return err
	}
	secrets := bearerValues(w.env)
	if check.code == "" {
		first, err := readBatch(r, nil)
		if err != nil {
			return err
		}
		if first.NextURI == "" || first.Token == "" {
			return fmt.Errorf("successful audit query lacked a continuation")
		}
		if err := rows(first, []any{"250.00"}); err != nil {
			return err
		}
		second, err := readBatch(w.next(ctx, reader, first))
		if err != nil {
			return err
		}
		if err := rows(second, []any{"89.50"}); err != nil {
			return err
		}
		last, err := readBatch(w.next(ctx, reader, second))
		if err != nil {
			return err
		}
		if last.NextURI != "" {
			return fmt.Errorf("audit query did not exhaust")
		}
		if err := rows(last, []any{"1200.00"}); err != nil {
			return err
		}
		secrets = append(secrets, first.Token, second.Token, last.Token)
		if err := w.env.AssertLogsOmit(ctx, secrets...); err != nil {
			return err
		}
	} else if err := r.expectError(w.kind, check.status, check.code, check.message); err != nil {
		return err
	}
	operation := "POST /api/v1/fetch"
	if w.kind == mcpHTTP {
		operation = "tools/call execute_sql_query"
	}
	filter := url.Values{"correlation_id": {correlation}, "transport": {string(w.kind)}, "operation": {operation}}
	path := "/api/v1/audit/events?" + filter.Encode()
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		audit, err := w.restCall(lookupCtx, "operator", "", http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var page struct {
			Events []struct {
				ActorID       string  `json:"actorId"`
				TenantID      string  `json:"tenantId"`
				CorrelationID string  `json:"correlationId"`
				Transport     surface `json:"transport"`
				Operation     string  `json:"operation"`
				SourceID      string  `json:"dataSourceId"`
				Fingerprint   string  `json:"queryFingerprint"`
				Decision      string  `json:"decision"`
				Outcome       string  `json:"outcome"`
				Rows          int     `json:"rowsReturned"`
				Status        int     `json:"statusCode"`
			} `json:"events"`
			Total int `json:"total"`
		}
		if err := audit.decode(&page); err != nil {
			return err
		}
		if page.Total != 0 {
			if page.Total != 1 || len(page.Events) != 1 {
				return fmt.Errorf("expected exactly one correlated audit event")
			}
			event := page.Events[0]
			actor := w.env.Identities[check.actor]
			hash, err := hex.DecodeString(event.Fingerprint)
			if event.ActorID != actor.Subject || event.TenantID != actor.Tenant ||
				event.CorrelationID != correlation || event.Transport != w.kind ||
				event.Operation != operation || event.SourceID != source.ID ||
				event.Decision != check.decision || event.Outcome != check.outcome || event.Rows != check.rows ||
				(w.kind == rest && event.Status != check.status) || len(hash) != 32 || err != nil {
				return fmt.Errorf("audit identity, correlation, outcome or fingerprint differs")
			}
			return safePayload(audit, append(secrets, check.sql, "250.00", "89.50", "1200.00")...)
		}
		select {
		case <-lookupCtx.Done():
			return fmt.Errorf("correlated audit event did not appear")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func bearerValues(env *reference.Environment) []string {
	var tokens []string
	for _, identity := range env.Identities {
		tokens = append(tokens, identity.RESTToken, identity.MCPToken)
	}
	return tokens
}

func safePayload(r reply, omitted ...string) error {
	raw := string(r.data)
	if containsAny(raw, omitted...) || containsAny(strings.ToLower(raw),
		"\"password\"", "\"secret\"", "\"token\"", "\"accesstoken\"", "\"refreshtoken\"",
		"\"sql\"", "\"rows\"", "\"entries\"", "\"dsn\"", "postgres://", "postgresql://", "password=") {
		return fmt.Errorf("response contains protected fields or fixture values")
	}
	return nil
}

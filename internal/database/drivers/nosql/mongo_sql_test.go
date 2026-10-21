package nosql

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"vitess.io/vitess/go/vt/sqlparser"
)

func TestMongoQueryMapsSQLToNativeFindAndStreamsRows(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().CreateClient(false))
	mt.RunOpts("native find", mtest.NewOptions().ClientType(mtest.Mock).DatabaseName("analytics"), func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "analytics.customers", mtest.FirstBatch,
			bson.D{{Key: "_id", Value: int32(1)}, {Key: "name", Value: "Ada"}, {Key: "age", Value: int32(37)}, {Key: "active", Value: true}},
		))
		conn := &mongoSQLConn{client: mt.Client, database: mt.DB}
		rows, err := conn.QueryContext(context.Background(),
			"SELECT name AS person FROM customers WHERE age >= ? AND active = true ORDER BY age DESC LIMIT ? OFFSET ?",
			[]driver.NamedValue{{Ordinal: 1, Value: int64(18)}, {Ordinal: 2, Value: int64(1)}, {Ordinal: 3, Value: int64(2)}},
		)
		if err != nil {
			t.Fatalf("QueryContext() error = %v", err)
		}
		defer rows.Close()
		if got := rows.Columns(); len(got) != 1 || got[0] != "person" {
			t.Fatalf("columns = %v, want [person]", got)
		}
		columnTypes, ok := rows.(interface{ ColumnTypeDatabaseTypeName(int) string })
		if !ok {
			t.Fatal("MongoDB rows do not expose column type metadata")
		}
		if got := columnTypes.ColumnTypeDatabaseTypeName(0); got != "VARCHAR" {
			t.Fatalf("column type = %q, want VARCHAR", got)
		}
		values := make([]driver.Value, 1)
		if err := rows.Next(values); err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if values[0] != "Ada" {
			t.Fatalf("row = %#v, want Ada", values)
		}
		if err := rows.Next(values); !errors.Is(err, io.EOF) {
			t.Fatalf("second Next() error = %v, want EOF", err)
		}

		event := mt.GetStartedEvent()
		if event == nil || event.CommandName != "find" {
			t.Fatalf("Mongo command = %v, want native find", event)
		}
		if event.Command.Lookup("find").StringValue() != "customers" {
			t.Fatalf("find command = %v", event.Command)
		}
		if event.Command.Lookup("skip").Int64() != 2 || event.Command.Lookup("limit").Int64() != 1 {
			t.Fatalf("find pagination = %v", event.Command)
		}
		if event.Command.Lookup("sort").Document().Lookup("age").Int32() != -1 {
			t.Fatalf("find sort = %v", event.Command.Lookup("sort"))
		}
		if event.Command.Lookup("projection").Document().Lookup("name").Int32() != 1 {
			t.Fatalf("find projection = %v", event.Command.Lookup("projection"))
		}
		var filter bson.D
		if err := bson.Unmarshal(event.Command.Lookup("filter").Document(), &filter); err != nil {
			t.Fatalf("decode find filter: %v", err)
		}
		if len(filter) != 1 || filter[0].Key != "$and" {
			t.Fatalf("find filter = %v, want both SQL predicates combined with $and", filter)
		}
	})
}

func TestMongoCountUsesNativeCountDocumentsAndDerivedQuerySemantics(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().CreateClient(false))
	mt.RunOpts("count", mtest.NewOptions().ClientType(mtest.Mock).DatabaseName("analytics"), func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "analytics.records", mtest.FirstBatch, bson.D{{Key: "n", Value: int64(3)}}))
		conn := &mongoSQLConn{client: mt.Client, database: mt.DB}
		rows, err := conn.QueryContext(context.Background(),
			"SELECT COUNT(*) AS total FROM (SELECT id FROM records WHERE active = true LIMIT 5) AS result",
			nil,
		)
		if err != nil {
			t.Fatalf("QueryContext() count error = %v", err)
		}
		defer rows.Close()
		if got := rows.Columns(); len(got) != 1 || got[0] != "total" {
			t.Fatalf("count columns = %v, want [total]", got)
		}
		values := make([]driver.Value, 1)
		if err := rows.Next(values); err != nil {
			t.Fatalf("read count: %v", err)
		}
		if values[0] != int64(3) {
			t.Fatalf("count value = %#v, want int64(3)", values[0])
		}
		if err := rows.Next(values); !errors.Is(err, io.EOF) {
			t.Fatalf("second count row error = %v, want EOF", err)
		}
		event := mt.GetStartedEvent()
		if event == nil || event.CommandName != "aggregate" {
			t.Fatalf("count command = %v, want native aggregate/countDocuments", event)
		}
		if event.Command.Lookup("aggregate").StringValue() != "records" {
			t.Fatalf("aggregate command = %v", event.Command)
		}
	})
}

func TestMongoSQLRejectsUnsupportedStatementsAndHonorsCancellation(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().CreateClient(false))
	mt.RunOpts("reject and cancel", mtest.NewOptions().ClientType(mtest.Mock).DatabaseName("analytics"), func(mt *mtest.T) {
		conn := &mongoSQLConn{client: mt.Client, database: mt.DB}
		if _, err := conn.QueryContext(context.Background(), "DELETE FROM records", nil); err == nil {
			t.Fatal("DELETE was accepted")
		}
		if _, err := conn.QueryContext(context.Background(), "SELECT id INTO OUTFILE '/tmp/export.csv' FROM records", nil); err == nil {
			t.Fatal("SELECT INTO was accepted")
		}
		if _, err := conn.QueryContext(context.Background(), "SELECT * FROM records WHERE active = true OR age > 20", nil); err == nil {
			t.Fatal("unsupported OR predicate was accepted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := conn.QueryContext(ctx, "SELECT * FROM records", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled QueryContext() error = %v, want context.Canceled", err)
		}
		if events := mt.GetAllStartedEvents(); len(events) != 0 {
			t.Fatalf("rejected or cancelled SQL reached MongoDB: %v", events)
		}
	})
}

func TestMongoSQLPingSendsNativePing(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().CreateClient(false))
	mt.RunOpts("native ping", mtest.NewOptions().ClientType(mtest.Mock).DatabaseName("analytics"), func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateSuccessResponse())
		conn := &mongoSQLConn{client: mt.Client, database: mt.DB}
		if err := conn.Ping(context.Background()); err != nil {
			t.Fatalf("Ping() error = %v", err)
		}
		event := mt.GetStartedEvent()
		if event == nil || event.CommandName != "ping" {
			t.Fatalf("Mongo command = %v, want ping", event)
		}
	})
}

func TestMongoSQLDerivedPaginationAndCountBoundaries(t *testing.T) {
	parsePlan := func(query string) (*mongoSelectPlan, error) {
		stmt, err := sqlparser.NewTestParser().Parse(query)
		if err != nil {
			return nil, err
		}
		selectStmt, ok := stmt.(*sqlparser.Select)
		if !ok {
			return nil, errors.New("expected SELECT")
		}
		index := new(int)
		return compileMongoSelect(selectStmt, nil, index)
	}

	page, err := parsePlan("SELECT * FROM (SELECT name FROM customers LIMIT 10 OFFSET 4) AS page LIMIT 20 OFFSET 3")
	if err != nil {
		t.Fatalf("compile derived pagination: %v", err)
	}
	if page.skip != 7 || !page.hasLimit || page.limit != 7 {
		t.Fatalf("composed pagination = skip %d, limit %d, hasLimit %t; want skip 7, limit 7", page.skip, page.limit, page.hasLimit)
	}
	limited, err := parsePlan("SELECT id FROM customers LIMIT 0")
	if err != nil {
		t.Fatalf("compile empty projection: %v", err)
	}
	rows, err := (&mongoSQLConn{}).execute(context.Background(), limited)
	if err != nil {
		t.Fatalf("execute zero-row query: %v", err)
	}
	if got := rows.(interface{ ColumnTypeDatabaseTypeName(int) string }).ColumnTypeDatabaseTypeName(0); got != "UNKNOWN" {
		t.Fatalf("empty result type = %q, want UNKNOWN", got)
	}

	count, err := parsePlan("SELECT COUNT(*) FROM (SELECT name FROM customers LIMIT 10 OFFSET 4) AS result LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("compile derived count: %v", err)
	}
	if !count.count || count.countInner == nil || count.countInner.skip != 4 || count.countInner.limit != 10 || count.skip != 1 || count.limit != 1 || !count.hasLimit {
		t.Fatalf("derived count plan = %+v; want inner pagination separate from outer count-row offset", count)
	}

	directCount, err := parsePlan("SELECT COUNT(*) FROM customers LIMIT 2 OFFSET 1")
	if err != nil {
		t.Fatalf("compile direct count: %v", err)
	}
	if directCount.skip != 1 || directCount.limit != 2 || !directCount.hasLimit {
		t.Fatalf("direct count row pagination = %+v", directCount)
	}
}

func TestMongoSQLRejectsMismatchedColumnQualifiersAndPreservesNullSemantics(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().CreateClient(false))
	mt.RunOpts("qualifier and null semantics", mtest.NewOptions().ClientType(mtest.Mock).DatabaseName("analytics"), func(mt *mtest.T) {
		conn := &mongoSQLConn{client: mt.Client, database: mt.DB}
		if _, err := conn.QueryContext(context.Background(), "SELECT c.name FROM customers c WHERE other.active = true", nil); err == nil {
			t.Fatal("mismatched qualifier was accepted")
		}
		if events := mt.GetAllStartedEvents(); len(events) != 0 {
			t.Fatalf("invalid qualifier reached MongoDB: %v", events)
		}
	})

	stmt, err := sqlparser.NewTestParser().Parse("SELECT id FROM customers WHERE nickname <> NULL")
	if err != nil {
		t.Fatalf("parse NULL comparison: %v", err)
	}
	plan, err := compileMongoSelect(stmt.(*sqlparser.Select), nil, new(int))
	if err != nil {
		t.Fatalf("compile NULL comparison: %v", err)
	}
	falseExpr, ok := plan.filter[0].Value.(bson.D)
	if len(plan.filter) != 1 || plan.filter[0].Key != "$expr" || !ok || falseExpr[0].Key != "$eq" {
		t.Fatalf("SQL comparison to NULL must be always unknown in WHERE; Mongo filter = %v", plan.filter)
	}
	conn := &mongoSQLConn{}
	if _, err := conn.QueryContext(context.Background(), "SELECT id FROM customers WHERE id IN ?", []driver.NamedValue{{Ordinal: 1, Value: []int64{1, 2}}}); err == nil {
		t.Fatal("IN with one array bind parameter was accepted instead of an explicit value list")
	}
}

func TestMongoSQLCancellationAfterCursorOpen(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().CreateClient(false))
	mt.RunOpts("cancel after cursor open", mtest.NewOptions().ClientType(mtest.Mock).DatabaseName("analytics"), func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, "analytics.records", mtest.FirstBatch, bson.D{{Key: "id", Value: int32(1)}}),
		)
		conn := &mongoSQLConn{client: mt.Client, database: mt.DB}
		ctx, cancel := context.WithCancel(context.Background())
		rows, err := conn.QueryContext(ctx, "SELECT id FROM records WHERE id = 1", nil)
		if err != nil {
			cancel()
			t.Fatalf("QueryContext() error = %v", err)
		}
		cancel()
		dest := make([]driver.Value, 1)
		if err := rows.Next(dest); !errors.Is(err, context.Canceled) {
			t.Fatalf("Next() after cancellation = %v, want context.Canceled", err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("Close() after cancellation: %v", err)
		}
	})
}

func TestMongoSQLUnfilteredSelectUsesEmptyFilterDocument(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().CreateClient(false))
	mt.RunOpts("empty filter document", mtest.NewOptions().ClientType(mtest.Mock).DatabaseName("analytics"), func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "analytics.records", mtest.FirstBatch,
			bson.D{{Key: "id", Value: int32(7)}},
		))
		conn := &mongoSQLConn{client: mt.Client, database: mt.DB}
		rows, err := conn.QueryContext(context.Background(), "SELECT id FROM records", nil)
		if err != nil {
			t.Fatalf("unfiltered QueryContext() error = %v", err)
		}
		defer rows.Close()
		values := make([]driver.Value, 1)
		if err := rows.Next(values); err != nil || values[0] != int64(7) {
			t.Fatalf("unfiltered row = %#v, error = %v", values, err)
		}
		event := mt.GetStartedEvent()
		if event == nil || event.CommandName != "find" {
			t.Fatalf("Mongo command = %v, want find", event)
		}
		var filter bson.D
		if err := bson.Unmarshal(event.Command.Lookup("filter").Document(), &filter); err != nil {
			t.Fatalf("decode filter document: %v", err)
		}
		if len(filter) != 0 {
			t.Fatalf("unfiltered query sent filter %v, want empty document", filter)
		}
	})
}

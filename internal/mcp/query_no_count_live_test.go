package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/controller"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	service "github.com/RhinoLink-Tech/data-fabric/internal/service"
	unified "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	protocol "github.com/mark3labs/mcp-go/mcp"
)

// This test requires disposable CI databases; generated identifiers are unique
// and the fixture table is dropped. It never uses a pre-existing local database.
func TestLiveNoCountRESTAndMCP(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("DF_NOCOUNT_ACCEPTANCE") != "github-ci" {
		t.Skip("isolated GitHub CI acceptance required")
	}
	pgDSN, mysqlHost, mariaHost := os.Getenv("DATA_FABRIC_POSTGRES_CONTRACT_DSN"), os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_HOST"), os.Getenv("DATA_FABRIC_MARIADB_CONTRACT_HOST")
	if pgDSN == "" && mysqlHost == "" && mariaHost == "" {
		t.Skip("isolated PostgreSQL, MySQL and MariaDB contract fixtures required")
	}
	if pgDSN == "" || mysqlHost == "" || mariaHost == "" {
		t.Fatal("isolated PostgreSQL, MySQL and MariaDB contract fixtures are required")
	}
	pg, err := url.Parse(pgDSN)
	if err != nil || pg.User == nil || pg.Hostname() != "localhost" || strings.TrimPrefix(pg.Path, "/") != "data_fabric_contract" || pg.User.Username() != "data_fabric_contract" {
		t.Fatal("invalid isolated PostgreSQL fixture")
	}
	pgPass, ok := pg.User.Password()
	if !ok {
		t.Fatal("missing PostgreSQL fixture password")
	}
	pgPort, err := strconv.Atoi(pg.Port())
	if err != nil {
		t.Fatal("invalid PostgreSQL port")
	}
	myPort, err := strconv.Atoi(os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_PORT"))
	if err != nil {
		t.Fatal("invalid MySQL port")
	}
	myUser, myPass, myDB := os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_USERNAME"), os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_PASSWORD"), os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_DATABASE")
	if mysqlHost != "127.0.0.1" || myUser != "data_fabric_contract" || myPass == "" || myDB != "data_fabric_contract" {
		t.Fatal("invalid isolated MySQL fixture")
	}
	mysqlConfig := mysql.NewConfig()
	mysqlConfig.User = myUser
	mysqlConfig.Passwd = myPass
	mysqlConfig.Net = "tcp"
	mysqlConfig.Addr = net.JoinHostPort(mysqlHost, strconv.Itoa(myPort))
	mysqlConfig.DBName = myDB
	mariaPort, err := strconv.Atoi(os.Getenv("DATA_FABRIC_MARIADB_CONTRACT_PORT"))
	if err != nil {
		t.Fatal("invalid MariaDB port")
	}
	mariaUser, mariaPass, mariaDB := os.Getenv("DATA_FABRIC_MARIADB_CONTRACT_USERNAME"), os.Getenv("DATA_FABRIC_MARIADB_CONTRACT_PASSWORD"), os.Getenv("DATA_FABRIC_MARIADB_CONTRACT_DATABASE")
	if mariaHost != "127.0.0.1" || mariaUser != "data_fabric_contract" || mariaPass == "" || mariaDB != "data_fabric_contract" {
		t.Fatal("invalid isolated MariaDB fixture")
	}
	mariaConfig := mysql.NewConfig()
	mariaConfig.User, mariaConfig.Passwd, mariaConfig.Net = mariaUser, mariaPass, "tcp"
	mariaConfig.Addr, mariaConfig.DBName = net.JoinHostPort(mariaHost, strconv.Itoa(mariaPort)), mariaDB
	fixtures := []struct {
		name, driver, dsn string
		kind              model.DatabaseType
		config            model.DataSourceConfig
	}{
		{"postgresql", "postgres", pgDSN, model.DatabaseTypePostgreSQL, model.DataSourceConfig{Host: pg.Hostname(), Port: pgPort, Database: strings.TrimPrefix(pg.Path, "/"), Username: pg.User.Username(), Password: pgPass, SSLMode: "disable"}},
		{"mysql", "mysql", mysqlConfig.FormatDSN(), model.DatabaseTypeMySQL, model.DataSourceConfig{Host: mysqlHost, Port: myPort, Database: myDB, Username: myUser, Password: myPass}},
		{"mariadb", "mysql", mariaConfig.FormatDSN(), model.DatabaseTypeMariaDB, model.DataSourceConfig{Host: mariaHost, Port: mariaPort, Database: mariaDB, Username: mariaUser, Password: mariaPass}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			db, err := sql.Open(fixture.driver, fixture.dsn)
			if err != nil {
				t.Fatal("open isolated fixture failed")
			}
			t.Cleanup(func() { _ = db.Close() })
			// Keep repeated letters in every real-source identifier; this catches validator
			// false positives deterministically while retaining a unique fixture name.
			table := "df_nocount_aaa_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			if _, err = db.Exec("CREATE TABLE " + table + " (id INTEGER NOT NULL)"); err != nil {
				t.Fatalf("create fixture: %v", err)
			}
			t.Cleanup(func() {
				if _, err := db.Exec("DROP TABLE " + table); err != nil {
					t.Errorf("drop fixture: %v", err)
				}
			})
			for i := 1; i <= 5; i++ {
				if _, err := db.Exec(fmt.Sprintf("INSERT INTO %s VALUES (%d)", table, i)); err != nil {
					t.Fatalf("seed fixture: %v", err)
				}
			}
			for _, mode := range []string{"pagination", "streaming"} {
				for _, api := range []string{"REST", "MCP"} {
					t.Run(mode+"/"+api, func(t *testing.T) {
						pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
						defer pool.CloseAll()
						source := &model.DataSource{ID: uuid.NewString(), Type: fixture.kind, Status: model.DataSourceStatusActive, Config: fixture.config}
						repo := describeTableRepo{source: source}
						tr := sql_translator.NewSQLTranslationManager(false)
						paging := service.NewQueryService(repo, pool, false, tr)
						defer paging.Shutdown()
						streaming := service.NewStreamingService(repo, pool, tr)
						defer streaming.Shutdown()
						qs := unified.NewUnifiedQueryService(paging, streaming, &config.Config{Query: config.QueryConfig{ExecutionMode: mode}})
						mcp := NewMCPServer(qs, repo, nil, tr)
						rest := controller.NewQueryController(qs)
						placeholder := "?"
						if fixture.kind == model.DatabaseTypePostgreSQL {
							placeholder = "$1"
						}
						query := "SELECT id FROM " + table + " WHERE id > " + placeholder + " ORDER BY id LIMIT 4"
						args := map[string]any{"datasource_id": source.ID, "sql": query, "parameters": []interface{}{int64(0)}, "batch_size": 2, "count_mode": "none", "execution_mode": mode}
						var page map[string]interface{}
						decodeMCP := func(res *protocol.CallToolResult, err error, success bool) map[string]interface{} {
							t.Helper()
							if err != nil || res == nil || res.IsError == success {
								t.Fatalf("MCP result=%+v err=%v success=%v", res, err, success)
							}
							if !success {
								return nil
							}
							var out map[string]interface{}
							if err := json.Unmarshal([]byte(describeTableText(t, res)), &out); err != nil {
								t.Fatal(err)
							}
							return out
						}
						restCall := func(method, path string, body []byte, params gin.Params, success bool) map[string]interface{} {
							t.Helper()
							rec := httptest.NewRecorder()
							ctx, _ := gin.CreateTestContext(rec)
							ctx.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
							ctx.Params = params
							if method == http.MethodPost {
								rest.FetchQuery(ctx)
							} else {
								rest.FetchNextBatch(ctx)
							}
							if (rec.Code == http.StatusOK) != success {
								t.Fatalf("REST status=%d body=%s", rec.Code, rec.Body.String())
							}
							if !success {
								return nil
							}
							var envelope struct {
								Data map[string]interface{} `json:"data"`
							}
							if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
								t.Fatal(err)
							}
							return envelope.Data
						}
						if api == "MCP" {
							res, err := mcp.handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", args))
							page = decodeMCP(res, err, true)
						} else {
							payload, _ := json.Marshal(map[string]any{"dataSourceId": source.ID, "sql": query, "parameters": []interface{}{int64(0)}, "batch_size": 2, "count_mode": "none"})
							page = restCall(http.MethodPost, "/api/v1/fetch", payload, nil, true)
						}
						seen, pages := 0, 0
						for {
							pages++
							more, ok := page["hasMore"].(bool)
							if !ok || page["countMode"] != "none" {
								t.Fatalf("missing no-count indicators: %v", page)
							}
							total, exists := page["totalCount"]
							if !exists || total != nil {
								t.Fatalf("unknown total not null: %v", page)
							}
							rows, ok := page["entries"].([]interface{})
							if !ok {
								t.Fatalf("missing entries: %v", page)
							}
							for _, item := range rows {
								row, ok := item.([]interface{})
								if !ok || len(row) != 1 {
									t.Fatalf("row=%v", item)
								}
								seen++
								if row[0] != float64(seen) {
									t.Fatalf("row=%v want=%d", row, seen)
								}
							}
							nextURI, _ := page["nextUri"].(string)
							if more != (nextURI != "") {
								t.Fatalf("bad continuation: %v", page)
							}
							if !more {
								break
							}
							if pages > 10 {
								t.Fatal("unbounded continuation")
							}
							id, slug, token := page["queryId"].(string), page["slug"].(string), page["token"].(string)
							if api == "MCP" {
								request := callToolRequest("fetch_next_batch", map[string]any{"query_id": id, "slug": slug, "token": token, "batch_size": 1})
								res, err := mcp.handleFetchNextBatch(context.Background(), request)
								page = decodeMCP(res, err, true)
								res, err = mcp.handleFetchNextBatch(context.Background(), request)
								decodeMCP(res, err, false)
							} else {
								params := gin.Params{{Key: "query_id", Value: id}, {Key: "slug", Value: slug}, {Key: "token", Value: token}}
								page = restCall(http.MethodGet, "/api/v1/fetch?batch_size=1", nil, params, true)
								restCall(http.MethodGet, "/api/v1/fetch?batch_size=1", nil, params, false)
							}
						}
						if seen != 4 {
							t.Fatalf("got %d rows, want 4", seen)
						}

					})
				}
			}
		})
	}
}

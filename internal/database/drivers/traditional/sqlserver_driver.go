package traditional

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"

	_ "github.com/denisenkom/go-mssqldb"
)

// SQLServerDriver implements the Driver interface for Microsoft SQL Server
type SQLServerDriver struct {
	base *drivers.DriverBase
}

// NewSQLServerDriver creates a new SQL Server driver instance
func NewSQLServerDriver() *SQLServerDriver {
	return &SQLServerDriver{
		base: drivers.NewDriverBase(model.DatabaseTypeSQLServer, drivers.CategoryRelational),
	}
}

// ApplyBatchPagination applies pagination to SQL query for batch processing
func (sd *SQLServerDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	sql = strings.TrimSpace(sql)
	if hasTopLevelSQLKeyword(sql, "OFFSET") || hasTopLevelSQLKeyword(sql, "FETCH") || hasTopLevelSQLKeyword(sql, "TOP") {
		// Apply a second page to the result of an already paginated statement.
		sql = fmt.Sprintf("SELECT * FROM (%s) AS batch_query", sql)
	}
	if !hasTopLevelOrderBy(sql) {
		// SQL Server requires ORDER BY for OFFSET/FETCH. A nested ORDER BY does
		// not order the outer query, so only a top-level clause is sufficient.
		sql += " ORDER BY (SELECT NULL)"
	}
	return fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", sql, offset, batchSize), nil
}

// findTopLevelSQLKeyword skips nested expressions, quoted text, and SQL
// comments so pagination keywords inside a CTE or string do not affect the
// outer statement.
func findTopLevelSQLKeyword(query, keyword string, start int) int {
	depth := 0
	for index := 0; index < len(query); {
		switch query[index] {
		case 39, 34, 96:
			index = skipSQLQuoted(query, index, query[index])
		case '[':
			index = skipSQLBracketIdentifier(query, index)
		case '-':
			if index+1 < len(query) && query[index+1] == '-' {
				index += 2
				for index < len(query) && query[index] != 10 {
					index++
				}
			} else {
				index++
			}
		case '/':
			if index+1 < len(query) && query[index+1] == '*' {
				index += 2
				for index+1 < len(query) && !(query[index] == '*' && query[index+1] == '/') {
					index++
				}
				if index+1 < len(query) {
					index += 2
				}
			} else {
				index++
			}
		case '(':
			depth++
			index++
		case ')':
			if depth > 0 {
				depth--
			}
			index++
		default:
			if index >= start && depth == 0 && index+len(keyword) <= len(query) &&
				strings.EqualFold(query[index:index+len(keyword)], keyword) &&
				(index == 0 || !isSQLWordByte(query[index-1])) &&
				(index+len(keyword) == len(query) || !isSQLWordByte(query[index+len(keyword)])) {
				return index
			}
			index++
		}
	}
	return -1
}

func hasTopLevelSQLKeyword(query, keyword string) bool {
	return findTopLevelSQLKeyword(query, keyword, 0) >= 0
}

func hasTopLevelOrderBy(query string) bool {
	for start := 0; start < len(query); {
		index := findTopLevelSQLKeyword(query, "ORDER", start)
		if index < 0 {
			return false
		}
		afterOrder := index + len("ORDER")
		for afterOrder < len(query) && isSQLSpace(query[afterOrder]) {
			afterOrder++
		}
		if afterOrder+len("BY") <= len(query) &&
			strings.EqualFold(query[afterOrder:afterOrder+len("BY")], "BY") &&
			(afterOrder+len("BY") == len(query) || !isSQLWordByte(query[afterOrder+len("BY")])) {
			return true
		}
		start = afterOrder
	}
	return false
}

func skipSQLQuoted(query string, start int, quote byte) int {
	for index := start + 1; index < len(query); index++ {
		if query[index] == quote {
			if index+1 < len(query) && query[index+1] == quote {
				index++
				continue
			}
			return index + 1
		}
	}
	return len(query)
}

func skipSQLBracketIdentifier(query string, start int) int {
	for index := start + 1; index < len(query); index++ {
		if query[index] == ']' {
			if index+1 < len(query) && query[index+1] == ']' {
				index++
				continue
			}
			return index + 1
		}
	}
	return len(query)
}

func isSQLSpace(value byte) bool {
	return value == ' ' || value == 9 || value == 10 || value == 13
}

func isSQLWordByte(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

// Open opens a connection to SQL Server database
func (sd *SQLServerDriver) Open(dsn string) (*sql.DB, error) {
	// Use the mssql driver to connect to SQL Server
	db, err := sql.Open("mssql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SQL Server: %v", err)
	}

	return db, nil
}

// ValidateDSN validates the SQL Server connection string
func (sd *SQLServerDriver) ValidateDSN(dsn string) error {
	// Parse DSN and validate its format
	parsedURL, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("invalid DSN format: %v", err)
	}

	// Check if it's using sqlserver scheme
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "sqlserver" && scheme != "mssql" {
		return fmt.Errorf("invalid scheme in DSN: %s, expected 'sqlserver' or 'mssql'", scheme)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("missing host in DSN")
	}

	// Check for authentication parameters
	if parsedURL.User == nil && !strings.Contains(dsn, "user id=") && !strings.Contains(dsn, "user id=") {
		return fmt.Errorf("missing username in DSN")
	}

	return nil
}

// GetDefaultPort returns the default port for SQL Server
func (sd *SQLServerDriver) GetDefaultPort() int {
	return 1433 // Default SQL Server port
}

// BuildDSN builds a SQL Server connection string from configuration
func (sd *SQLServerDriver) BuildDSN(config *model.DataSourceConfig) string {
	// Build SQL Server connection string
	// Format: sqlserver://user:password@host:port?database=dbname
	var dsn string
	if config.SSL {
		dsn = fmt.Sprintf("sqlserver://%s:%s@%s:%d?database=%s&encrypt=true",
			config.Username,
			url.QueryEscape(config.Password),
			config.Host,
			config.Port,
			config.Database,
		)
	} else {
		dsn = fmt.Sprintf("sqlserver://%s:%s@%s:%d?database=%s&encrypt=false",
			config.Username,
			url.QueryEscape(config.Password),
			config.Host,
			config.Port,
			config.Database,
		)
	}

	// Add additional properties
	if config.AdditionalProps != nil {
		params := url.Values{}
		for k, v := range config.AdditionalProps {
			params.Add(k, fmt.Sprintf("%v", v))
		}
		// Append additional params to the DSN
		if params.Encode() != "" {
			dsn += "&" + params.Encode()
		}
	}

	return dsn
}

// GetDatabaseTypeName returns the database type name
func (sd *SQLServerDriver) GetDatabaseTypeName() string {
	return sd.base.GetDatabaseTypeName()
}

// TestConnection tests if the SQL Server connection is working
func (sd *SQLServerDriver) TestConnection(db *sql.DB) error {
	// Execute a simple query to test the connection
	query := "SELECT 1"
	row := db.QueryRow(query)
	var result int
	err := row.Scan(&result)
	if err != nil {
		return fmt.Errorf("failed to execute test query: %v", err)
	}

	if result != 1 {
		return fmt.Errorf("unexpected result from test query: %d", result)
	}

	return nil
}

// GetDriverName returns the underlying SQL driver name for SQL Server
func (sd *SQLServerDriver) GetDriverName() string {
	return "mssql"
}

// GetCategory returns the driver category
func (sd *SQLServerDriver) GetCategory() drivers.DriverCategory {
	return sd.base.GetCategory()
}

// GetCapabilities returns driver capabilities
func (sd *SQLServerDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     true,  // SQL Server supports full ACID transactions
		SupportsSchemaDiscovery: true,  // SQL Server supports schema introspection
		SupportsTimeTravel:      false, // SQL Server doesn't have built-in time travel like some other systems
		RequiresTokenRotation:   false, // SQL Server uses traditional authentication
		SupportsStreaming:       true,  // Supports streaming of large result sets
	}
}

// ConfigureAuth configures authentication for SQL Server (supports Windows Auth, SQL Auth, etc.)
func (sd *SQLServerDriver) ConfigureAuth(authConfig interface{}) error {
	// SQL Server supports various authentication methods like SQL Server Authentication, Windows Authentication, etc.
	// Implementation would depend on specific authConfig provided
	// This is a placeholder for future extension

	return nil
}

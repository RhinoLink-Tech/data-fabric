package readonly

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// mysqlWritePrivileges matches grant lines that indicate the account can
// write or administer. Word boundaries keep identifiers such as a column
// named update_time from matching.
var mysqlWritePrivileges = regexp.MustCompile(`\b(ALL PRIVILEGES|SUPER|FILE|INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|GRANT OPTION)\b`)

// VerifySession opens one enforced connection and reports whether the
// session is actually read-only, together with least-privilege warnings for
// the account (#20 acceptance: recommend/verify read-only accounts). It
// issues read-only statements only.
//
// ModeDSNParam types are opened through open with the read-only parameter
// already applied to dsn, preserving driver-specific opening behavior;
// ModeConnectorInit types are opened through OpenDB, the same enforced path
// the connection pool uses.
func VerifySession(ctx context.Context, dbType model.DatabaseType, open func(dsn string) (*sql.DB, error), dsn string) (enforced bool, warnings []string, err error) {
	var db *sql.DB
	switch ModeFor(dbType) {
	case ModeConnectorInit:
		db, _, err = OpenDB(dbType, dsn)
	case ModeDSNParam:
		var applied bool
		dsn, applied = ApplyDSN(dbType, dsn)
		if !applied {
			return false, nil, nil
		}
		db, err = open(dsn)
	default:
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("failed to open enforced connection: %w", err)
	}
	defer db.Close()
	return verifySessionOnDB(ctx, db, dbType)
}

// verifySessionOnDB reads the session read-only flag and inspects account
// privileges on an already-open connection.
func verifySessionOnDB(ctx context.Context, db *sql.DB, dbType model.DatabaseType) (bool, []string, error) {
	var enforced bool
	var err error
	switch ModeFor(dbType) {
	case ModeConnectorInit:
		enforced, err = verifyMySQLSession(ctx, db)
	case ModeDSNParam:
		enforced, err = verifyPostgresSession(ctx, db)
	default:
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return enforced, accountPrivilegeWarnings(ctx, db, dbType), nil
}

// verifyMySQLSession reads the session access mode; 1 means every
// transaction in the session is read-only.
func verifyMySQLSession(ctx context.Context, db *sql.DB) (bool, error) {
	var readOnly int
	if err := db.QueryRowContext(ctx, "SELECT @@SESSION.TRANSACTION_READ_ONLY").Scan(&readOnly); err != nil {
		return false, fmt.Errorf("failed to read session read-only flag: %w", err)
	}
	return readOnly != 0, nil
}

// verifyPostgresSession reads the effective transaction access mode.
func verifyPostgresSession(ctx context.Context, db *sql.DB) (bool, error) {
	var readOnly string
	if err := db.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&readOnly); err != nil {
		return false, fmt.Errorf("failed to read session read-only flag: %w", err)
	}
	return strings.EqualFold(strings.TrimSpace(readOnly), "on"), nil
}

// accountPrivilegeWarnings inspects the connected account for write or
// administrative privileges and recommends a least-privilege read-only
// account otherwise (#20). Inspection failures degrade to a warning instead
// of failing the check.
func accountPrivilegeWarnings(ctx context.Context, db *sql.DB, dbType model.DatabaseType) []string {
	switch ModeFor(dbType) {
	case ModeConnectorInit:
		return mysqlPrivilegeWarnings(ctx, db)
	case ModeDSNParam:
		return postgresPrivilegeWarnings(ctx, db)
	default:
		return nil
	}
}

func mysqlPrivilegeWarnings(ctx context.Context, db *sql.DB) []string {
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER()")
	if err != nil {
		return []string{"could not inspect account privileges: " + redactProbeError(err)}
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			continue
		}
		for _, match := range mysqlWritePrivileges.FindAllString(strings.ToUpper(grant), -1) {
			if _, dup := seen[match]; dup {
				continue
			}
			seen[match] = struct{}{}
		}
	}
	warnings := make([]string, 0, len(seen))
	for privilege := range seen {
		warnings = append(warnings, fmt.Sprintf("account holds write or administrative privilege %q; use a least-privilege read-only account (docs/readonly-access.md)", privilege))
	}
	return warnings
}

func postgresPrivilegeWarnings(ctx context.Context, db *sql.DB) []string {
	var super, createdb, createrole bool
	err := db.QueryRowContext(ctx,
		"SELECT rolsuper, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = current_user",
	).Scan(&super, &createdb, &createrole)
	if err != nil {
		return []string{"could not inspect account privileges: " + redactProbeError(err)}
	}

	var warnings []string
	for _, attr := range []struct {
		flag bool
		name string
	}{{super, "rolsuper"}, {createdb, "rolcreatedb"}, {createrole, "rolcreaterole"}} {
		if attr.flag {
			warnings = append(warnings, fmt.Sprintf("account role attribute %s is set; use a least-privilege read-only account (docs/readonly-access.md)", attr.name))
		}
	}
	return warnings
}

// Driver messages may contain arbitrary fragments or encodings of credentials.
// Privilege inspection errors are therefore never exposed to clients.
func redactProbeError(err error) string {
	return "privilege inspection failed"
}

package mysqlutil

import (
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestCustomCARequiresVerifiedTLS(t *testing.T) {
	for _, dsn := range []string{
		"user:secret@tcp(localhost:3306)/db?datafabricsslrootcert=%2Ftmp%2Fca.pem",
		"user:secret@tcp(localhost:3306)/db?tls=skip-verify&datafabricsslrootcert=%2Ftmp%2Fca.pem",
	} {
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Connector(cfg); err == nil {
			t.Fatal("unverified custom CA accepted")
		}
	}
}

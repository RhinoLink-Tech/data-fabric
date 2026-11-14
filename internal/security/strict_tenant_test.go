package security

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"testing"
)

func TestStrictTenantIsolation(t *testing.T) {
	ds := &model.DataSource{OwnerID: "alice", TenantID: "Team"}
	for _, tt := range []struct {
		name        string
		caller      *Caller
		read, write bool
	}{
		{"owner other tenant", &Caller{UserID: "alice", TenantID: "Other", StrictTenantIsolation: true}, false, false},
		{"owner changed case", &Caller{UserID: "alice", TenantID: "team", StrictTenantIsolation: true}, false, false},
		{"member same tenant", &Caller{UserID: "bob", TenantID: "Team", StrictTenantIsolation: true}, true, false},
		{"owner same tenant", &Caller{UserID: "alice", TenantID: "Team", StrictTenantIsolation: true}, true, true},
		{"missing tenant", &Caller{UserID: "alice", StrictTenantIsolation: true}, false, false},
		{"legacy compatibility", &Caller{UserID: "alice", TenantID: "Other"}, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.caller.CanRead(ds) != tt.read || tt.caller.CanWrite(ds) != tt.write {
				t.Fatal("unexpected access")
			}
		})
	}
}

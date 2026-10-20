package security

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// The #9 authorization matrix. Every combination of caller identity and
// record ownership the read/write gates must decide, pinned in one table so
// the rules in datasource_authz.go cannot drift silently.

func ownerSource() *model.DataSource {
	return &model.DataSource{ID: "ds-1", Name: "warehouse", OwnerID: "alice", TenantID: "acme"}
}

func TestCallerCanReadMatrix(t *testing.T) {
	tests := []struct {
		name   string
		caller *Caller
		source *model.DataSource
		want   bool
	}{
		// Admins and the authorization-disabled nil caller read everything.
		{"admin reads any owned row", &Caller{UserID: "root", Roles: []string{AdminRole}}, ownerSource(), true},
		{"nil caller reads any owned row", nil, ownerSource(), true},

		// Owner and tenant members.
		{"owner reads own row", &Caller{UserID: "alice"}, ownerSource(), true},
		{"tenant member reads tenant row", &Caller{UserID: "bob", TenantID: "acme"}, ownerSource(), true},

		// Foreign identities are denied.
		{"foreign user denied", &Caller{UserID: "eve", TenantID: "contoso"}, ownerSource(), false},
		{"owner keeps access with a foreign tenant claim", &Caller{UserID: "alice", TenantID: "contoso"}, ownerSource(), true},
		{"tenantless user denied on tenant row", &Caller{UserID: "bob"}, ownerSource(), false},

		// Legacy rows have no verifiable owner and are visible only to admins.
		{"legacy row denied to regular user", &Caller{UserID: "eve", TenantID: "contoso"}, &model.DataSource{ID: "legacy"}, false},
		{"legacy row readable by admin", &Caller{UserID: "root", Roles: []string{AdminRole}}, &model.DataSource{ID: "legacy"}, true},

		// The empty tenant is never matched as a tenant: a tenantless caller
		// does not gain access to other tenantless users' rows.
		{"tenantless caller denied on other tenantless row", &Caller{UserID: "bob"}, &model.DataSource{ID: "ds-2", OwnerID: "carol"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caller.CanRead(tt.source); got != tt.want {
				t.Fatalf("CanRead() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCallerCanWriteMatrix(t *testing.T) {
	tests := []struct {
		name   string
		caller *Caller
		source *model.DataSource
		want   bool
	}{
		{"admin writes any owned row", &Caller{UserID: "root", Roles: []string{AdminRole}}, ownerSource(), true},
		{"nil caller writes any owned row", nil, ownerSource(), true},
		{"owner writes own row", &Caller{UserID: "alice"}, ownerSource(), true},

		// Tenant membership grants read sharing only, not write sharing.
		{"tenant member cannot write tenant row", &Caller{UserID: "bob", TenantID: "acme"}, ownerSource(), false},
		{"foreign user cannot write", &Caller{UserID: "eve", TenantID: "contoso"}, ownerSource(), false},

		// Legacy rows are writable only by administrators.
		{"legacy row not writable by non-admin", &Caller{UserID: "bob"}, &model.DataSource{ID: "legacy"}, false},
		{"legacy row writable by admin", &Caller{UserID: "root", Roles: []string{AdminRole}}, &model.DataSource{ID: "legacy"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caller.CanWrite(tt.source); got != tt.want {
				t.Fatalf("CanWrite() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanReadAndCanWriteRejectNilDataSource(t *testing.T) {
	caller := &Caller{UserID: "root", Roles: []string{AdminRole}}
	if caller.CanRead(nil) || caller.CanWrite(nil) {
		t.Fatal("nil data source must never be readable or writable")
	}
}

func TestIsLegacyDataSource(t *testing.T) {
	tests := []struct {
		name   string
		source *model.DataSource
		want   bool
	}{
		{"no owner no tenant", &model.DataSource{ID: "legacy"}, true},
		{"owner only", &model.DataSource{OwnerID: "alice"}, false},
		{"tenant only", &model.DataSource{TenantID: "acme"}, false},
		{"both", ownerSource(), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLegacyDataSource(tt.source); got != tt.want {
				t.Fatalf("IsLegacyDataSource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStampCaller(t *testing.T) {
	t.Run("stamps caller identity", func(t *testing.T) {
		ds := &model.DataSource{Name: "warehouse"}
		StampCaller(ds, &Caller{UserID: "alice", TenantID: "acme"})
		if ds.OwnerID != "alice" || ds.TenantID != "acme" {
			t.Fatalf("ownership = %q/%q, want alice/acme", ds.OwnerID, ds.TenantID)
		}
	})
	t.Run("nil caller leaves the row legacy", func(t *testing.T) {
		ds := &model.DataSource{Name: "warehouse"}
		StampCaller(ds, nil)
		if !IsLegacyDataSource(ds) {
			t.Fatalf("authorization-disabled create must stay legacy: %+v", ds)
		}
	})
	t.Run("nil data source is a no-op", func(t *testing.T) {
		StampCaller(nil, &Caller{UserID: "alice"})
	})
}

func TestCallerHelpers(t *testing.T) {
	if sys := SystemCaller(); sys.UserID != SystemUserID || !sys.IsAdmin() {
		t.Fatalf("SystemCaller() = %+v, want admin %q", sys, SystemUserID)
	}

	if key := SystemCaller().Key(); key != SystemUserID {
		t.Fatalf("SystemCaller().Key() = %q, want %q", key, SystemUserID)
	}
	if key := (*Caller)(nil).Key(); key != "" {
		t.Fatalf("nil caller Key() = %q, want empty", key)
	}
	if (*Caller)(nil).IsAdmin() {
		t.Fatal("nil caller must not be admin")
	}

	claims := &Claims{UserID: "alice", TenantID: "acme", Roles: []string{"viewer", AdminRole}}
	caller := CallerFromClaims(claims)
	if caller.UserID != "alice" || caller.TenantID != "acme" || !caller.IsAdmin() {
		t.Fatalf("CallerFromClaims() = %+v", caller)
	}
	if nilCaller := CallerFromClaims(nil); nilCaller != nil {
		t.Fatalf("CallerFromClaims(nil) = %+v, want nil", nilCaller)
	}
}

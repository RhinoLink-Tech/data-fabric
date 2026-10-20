package security

import (
	"errors"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"

	"github.com/gin-gonic/gin"
)

// Data-source authorization (#9). Every request path that reads or mutates a
// stored data source resolves the record first and then asks these functions
// whether the caller may proceed. The rules live here only — the pagination
// and streaming services share this implementation so they cannot drift.

var (
	// ErrDataSourceAccessDenied means the caller may not access the data
	// source. Controllers answer missing and forbidden IDs with the same
	// response for non-admin callers so existence cannot be probed.
	ErrDataSourceAccessDenied = errors.New("data source access denied")
)

// SystemUserID identifies the MCP stdio caller. MCP runs as a trusted local
// process with administrator-equivalent access; remote identity arrives with
// the authenticated Streamable HTTP transport (#185).
const SystemUserID = "__system__"

// AdminRole grants unrestricted data-source access. Tokens must be minted by
// a trusted deployment identity provider or operator; anyone holding the
// signing secret can assert user, tenant, and role claims.
const AdminRole = "admin"

// Caller is the identity a request executes as. A nil Caller means
// authorization is not applicable — the management API running with
// security.enable_auth disabled, where no identity exists to check. Query
// paths always carry a real caller (HTTP fetch is authenticated and MCP uses
// SystemCaller), and handlers behind RequireAuth must not produce nil.
type Caller struct {
	UserID   string
	TenantID string
	Roles    []string
	Scopes   []string
}

// IsAdmin reports whether the caller holds the admin role.
func (c *Caller) IsAdmin() bool {
	return c != nil && CallerHasRole(c.Roles, AdminRole)
}

// CallerHasRole reports whether roles contains role exactly.
func CallerHasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// Key returns the identity sessions are bound to. The zero value is only
// reachable in the authorization-disabled mode described on Caller.
func (c *Caller) Key() string {
	if c == nil {
		return ""
	}
	return c.UserID
}

// SystemCaller returns the administrator-equivalent caller for trusted local
// channels (MCP stdio).
func SystemCaller() *Caller {
	return &Caller{UserID: SystemUserID, Roles: []string{AdminRole}}
}

// CallerFromClaims converts validated JWT claims. Nil claims yield a nil
// Caller (authorization not applicable).
func CallerFromClaims(claims *Claims) *Caller {
	if claims == nil {
		return nil
	}
	return &Caller{
		UserID:   claims.UserID,
		TenantID: claims.TenantID,
		Roles:    claims.Roles,
		Scopes:   strings.Fields(claims.Scope),
	}
}

// CallerFromGin extracts the caller from the request context.
func CallerFromGin(c *gin.Context) *Caller {
	claims, _ := GetUserClaims(c)
	return CallerFromClaims(claims)
}

// IsLegacyDataSource reports whether a record predates ownership tracking:
// no owner and no tenant. Only administrators can access legacy rows because
// their original owner cannot be established safely.
func IsLegacyDataSource(ds *model.DataSource) bool {
	return ds != nil && ds.OwnerID == "" && ds.TenantID == ""
}

// StampCaller records ownership on a newly created data source. Rows created
// while authorization is disabled stay legacy (empty owner and tenant).
func StampCaller(ds *model.DataSource, caller *Caller) {
	if ds == nil || caller == nil {
		return
	}
	ds.OwnerID = caller.UserID
	ds.TenantID = caller.TenantID
}

// CanRead reports whether the caller may read or query through the data
// source: admins read everything, owners read their own records, and members
// of a real tenant read that tenant's records. Legacy rows are admin-only
// because their original owner cannot be established safely.
func (c *Caller) CanRead(ds *model.DataSource) bool {
	if ds == nil {
		return false
	}
	if c == nil || c.IsAdmin() {
		return true
	}
	if ds.OwnerID != "" && ds.OwnerID == c.UserID {
		return true
	}
	if ds.TenantID != "" && ds.TenantID == c.TenantID {
		return true
	}
	return false
}

// CanWrite reports whether the caller may modify or delete the data source:
// only its owner or an admin. Same-tenant members get read sharing, not
// write sharing. Legacy rows are admin-only.
func (c *Caller) CanWrite(ds *model.DataSource) bool {
	if ds == nil {
		return false
	}
	if c == nil || c.IsAdmin() {
		return true
	}
	return ds.OwnerID != "" && ds.OwnerID == c.UserID
}

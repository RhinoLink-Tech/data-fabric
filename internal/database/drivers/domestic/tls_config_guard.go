package domestic

import (
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// The affected registry-created drivers do not yet map DataSourceConfig TLS
// options to a verified transport. Reject explicit requests rather than
// silently opening plaintext. A configured Kingbase constructor may retain
// its existing matching secure mode.
const unsupportedDomesticTLSDSN = "data-fabric-unsupported-tls"

func unsupportedDomesticTLS(config *model.DataSourceConfig, constructorMode string) bool {
	if config.SSLRootCert != "" || config.TLSCA != "" || config.TLSServerName != "" {
		return true
	}
	requested := strings.ToLower(strings.TrimSpace(config.SSLMode))
	actual := strings.ToLower(strings.TrimSpace(constructorMode))
	secure := actual == "require" || actual == "verify-ca" || actual == "verify-full"
	if requested == "" {
		return config.SSL && !secure
	}
	if requested == "disable" {
		return config.SSL
	}
	return !secure || requested != actual
}

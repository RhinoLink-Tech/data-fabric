package onboarding

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// RequireInnoDB rejects tables that cannot provide transaction rollback and row
// locks. Call after migrations and before exposing the onboarding runtime.
func RequireInnoDB(db *gorm.DB, names []string) error {
	if db == nil || db.Dialector == nil || db.Dialector.Name() != "mysql" || len(names) == 0 {
		return errors.New("transactional MySQL metadata required")
	}
	var rows []struct {
		TableName string `gorm:"column:table_name"`
		Engine    string `gorm:"column:engine"`
	}
	if err := db.Raw("SELECT TABLE_NAME AS table_name, ENGINE AS engine FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN ?", names).Scan(&rows).Error; err != nil {
		return errors.New("metadata table engine verification failed")
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if !strings.EqualFold(row.Engine, "innodb") {
			return errors.New("metadata table must use InnoDB")
		}
		seen[row.TableName] = true
	}
	for _, name := range names {
		if !seen[name] {
			return errors.New("metadata table missing")
		}
	}
	return nil
}

// RequiredTables includes every table involved in authenticated mutations,
// result recovery, session state, quotas and transactionally coupled auditing.
var RequiredTables = []string{"data_sources", "audit_events", "df_onboarding_identity_grants", "df_onboarding_flows", "df_onboarding_completions", "df_onboarding_quota_lock", "df_browser_login_states", "df_browser_sessions", "df_browser_auth_quota"}

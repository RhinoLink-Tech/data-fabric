package onboarding

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestRequireInnoDBRejectsNonMySQL(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if RequireInnoDB(db, []string{"data_sources"}) == nil || RequireInnoDB(nil, []string{"data_sources"}) == nil {
		t.Fatal("non-MySQL metadata accepted")
	}
}

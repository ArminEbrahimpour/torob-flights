package postgres

import (
	"path/filepath"
	"testing"
)

func TestMigrationFileBaseName(t *testing.T) {
	got := filepath.Base("/app/migrations/002_multi_collector.sql")
	if got != "002_multi_collector.sql" {
		t.Fatalf("got %q", got)
	}
}

// cmd/hash/migrate_test.go
package main

import (
	"bytes"
	"testing"
)

func TestMigrateStatus_NoState(t *testing.T) {
	var stdout bytes.Buffer
	err := runMigrateStatus(&stdout, "/nonexistent/migration.json")
	if err == nil {
		t.Error("expected error when no migration state exists")
	}
}

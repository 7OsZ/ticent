package database_test

import (
	"os"
	"testing"

	"ticent/internal/platform/database"
	"ticent/internal/platform/testutil"

	"github.com/stretchr/testify/require"
)

func TestRunMigrationWorksOutsideRepoRoot(t *testing.T) {
	_ = testutil.DB(t)

	// Pindah ke folder sementara kosong di luar repo untuk membuktikan migrasi memakai go:embed,
	// bukan membaca folder relatif file://migrations.
	t.Chdir(t.TempDir())
	require.NoError(t, database.RunMigration(os.Getenv("TEST_DATABASE_URL")))
}

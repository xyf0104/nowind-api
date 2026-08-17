package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPrepareFreshInstallBusinessSeedsSkipsDefaultsAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	fsys := fstest.MapFS{
		"001_schema.sql":                           {Data: []byte("CREATE TABLE example (id bigint);")},
		"008_seed_default_group.sql":               {Data: []byte(" INSERT INTO groups DEFAULT VALUES; \n")},
		"157_seed_default_channel_pricing.sql":     {Data: []byte("INSERT INTO channels DEFAULT VALUES;")},
		"173_seed_nowind_v1061_models_pricing.sql": {Data: []byte("INSERT INTO channel_model_pricing DEFAULT VALUES;")},
	}
	files := []string{
		"001_schema.sql",
		"008_seed_default_group.sql",
		"157_seed_default_channel_pricing.sql",
		"173_seed_nowind_v1061_models_pricing.sql",
	}

	mock.ExpectQuery("SELECT\\s+NOT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"fresh"}).AddRow(true))
	mock.ExpectBegin()
	for _, name := range files[1:] {
		content := string(fsys[name].Data)
		sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
		mock.ExpectExec("INSERT INTO schema_migrations").
			WithArgs(name, hex.EncodeToString(sum[:])).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}
	mock.ExpectCommit()

	require.NoError(t, prepareFreshInstallBusinessSeeds(context.Background(), db, fsys, files))
	mock.ExpectClose()
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareFreshInstallBusinessSeedsLeavesExistingInstallationUntouched(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	fsys := fstest.MapFS{
		"008_seed_default_group.sql": {Data: []byte("INSERT INTO groups DEFAULT VALUES;")},
	}
	mock.ExpectQuery("SELECT\\s+NOT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"fresh"}).AddRow(false))

	require.NoError(t, prepareFreshInstallBusinessSeeds(
		context.Background(),
		db,
		fsys,
		[]string{"008_seed_default_group.sql"},
	))
	mock.ExpectClose()
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareFreshInstallBusinessSeedsIgnoresUnrelatedMigrationSets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	fsys := fstest.MapFS{
		"001_schema.sql": {Data: []byte("CREATE TABLE example (id bigint);")},
	}
	require.NoError(t, prepareFreshInstallBusinessSeeds(
		context.Background(),
		db,
		fsys,
		[]string{"001_schema.sql"},
	))
	mock.ExpectClose()
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIsFreshInstallationPropagatesDatabaseErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT\\s+NOT EXISTS").WillReturnError(sql.ErrConnDone)
	fresh, err := isFreshInstallation(context.Background(), db)
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.False(t, fresh)
	mock.ExpectClose()
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

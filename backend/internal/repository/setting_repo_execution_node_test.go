package repository

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMigrateExecutionNodeDefaultWeights_UsesSourceNodeAndMigratesOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO settings").
		WithArgs(service.SettingKeyExecutionNodeDefaultWeightsMigrated).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("pending"))
	mock.ExpectQuery("SELECT value FROM settings WHERE key = \\$1 FOR UPDATE").
		WithArgs(service.SettingKeyExecutionNodeWeights).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"source-node":1,"api2":1}`))
	mock.ExpectExec("UPDATE settings SET value = \\$1, updated_at = NOW\\(\\) WHERE key = \\$2").
		WithArgs(`{"api2":1,"source-node":9}`, service.SettingKeyExecutionNodeWeights).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE settings SET value = \\$1, updated_at = NOW\\(\\) WHERE key = \\$2").
		WithArgs(`{"result":"migrated_9_to_1","source_node_id":"source-node"}`, service.SettingKeyExecutionNodeDefaultWeightsMigrated).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := &settingRepository{db: db}
	migrated, err := repo.MigrateExecutionNodeDefaultWeights(context.Background(), "source-node")
	require.NoError(t, err)
	require.True(t, migrated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrateExecutionNodeDefaultWeights_PreservesCustomPolicyAndClaimsOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO settings").
		WithArgs(service.SettingKeyExecutionNodeDefaultWeightsMigrated).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("pending"))
	mock.ExpectQuery("SELECT value FROM settings WHERE key = \\$1 FOR UPDATE").
		WithArgs(service.SettingKeyExecutionNodeWeights).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"source-node":3,"api2":1}`))
	mock.ExpectExec("UPDATE settings SET value = \\$1, updated_at = NOW\\(\\) WHERE key = \\$2").
		WithArgs(`{"result":"preserved_custom","source_node_id":"source-node"}`, service.SettingKeyExecutionNodeDefaultWeightsMigrated).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := &settingRepository{db: db}
	migrated, err := repo.MigrateExecutionNodeDefaultWeights(context.Background(), "source-node")
	require.NoError(t, err)
	require.False(t, migrated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrateExecutionNodeDefaultWeights_ReturnsWithoutClaimWhenAlreadyDone(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO settings").
		WithArgs(service.SettingKeyExecutionNodeDefaultWeightsMigrated).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	repo := &settingRepository{db: db}
	migrated, err := repo.MigrateExecutionNodeDefaultWeights(context.Background(), "source-node")
	require.NoError(t, err)
	require.False(t, migrated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrateExecutionNodeDefaultWeights_DoesNotConsumeMarkerBeforePeerExists(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO settings").
		WithArgs(service.SettingKeyExecutionNodeDefaultWeightsMigrated).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("pending"))
	mock.ExpectQuery("SELECT value FROM settings WHERE key = \\$1 FOR UPDATE").
		WithArgs(service.SettingKeyExecutionNodeWeights).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"source-node":1}`))
	mock.ExpectRollback()

	repo := &settingRepository{db: db}
	migrated, err := repo.MigrateExecutionNodeDefaultWeights(context.Background(), "source-node")
	require.NoError(t, err)
	require.False(t, migrated)
	require.NoError(t, mock.ExpectationsWereMet())
}

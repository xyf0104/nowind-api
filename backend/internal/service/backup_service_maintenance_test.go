package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackupService_MaintenanceLockIsShared(t *testing.T) {
	svc := &BackupService{}

	require.NoError(t, svc.beginMaintenance(maintenanceBackup))
	require.ErrorIs(t, svc.beginMaintenance(maintenanceRestore), ErrBackupInProgress)
	svc.finishMaintenance(maintenanceBackup)

	require.NoError(t, svc.beginMaintenance(maintenanceRestore))
	require.ErrorIs(t, svc.beginMaintenance(maintenanceBackup), ErrRestoreInProgress)
	svc.finishMaintenance(maintenanceRestore)

	require.NoError(t, svc.beginMaintenance(maintenanceBackup))
	svc.finishMaintenance(maintenanceBackup)
}

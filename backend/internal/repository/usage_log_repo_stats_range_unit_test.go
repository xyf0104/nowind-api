//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestGetAccountWindowStatsRangeUsesHalfOpenPointInTimeWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	costExpression := regexp.QuoteMeta(usageLogAccountCostExpression(""))
	mock.ExpectQuery(`(?s)SELECT\s+COUNT\(\*\) as requests.*COALESCE\(SUM\(`+costExpression+`\), 0\) as cost.*FROM usage_logs.*WHERE account_id = \$1 AND created_at >= \$2 AND created_at < \$3`).
		WithArgs(int64(42), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"requests", "tokens", "cost", "standard_cost", "user_cost"}).
			AddRow(int64(2), int64(300), 12.5, 10.0, 15.0))

	repo := &usageLogRepository{sql: db}
	stats, err := repo.GetAccountWindowStatsRange(context.Background(), 42, start, end)
	require.NoError(t, err)
	require.Equal(t, int64(2), stats.Requests)
	require.Equal(t, int64(300), stats.Tokens)
	require.Equal(t, 12.5, stats.Cost)
	require.NoError(t, mock.ExpectationsWereMet())
}

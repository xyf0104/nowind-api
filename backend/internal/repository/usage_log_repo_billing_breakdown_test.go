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

func TestGetAccountBillingUsersUsesCanonicalCostsAndHalfOpenRange(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 8, 13, 1, 15, 0, 0, time.UTC)
	end := start.Add(5 * time.Hour)

	costExpression := regexp.QuoteMeta(usageLogAccountCostExpression("ul"))
	mock.ExpectQuery(`(?s)COALESCE\(SUM\(`+costExpression+`\), 0\) AS account_cost.*COALESCE\(SUM\(ul\.actual_cost\), 0\) AS user_cost.*WHERE ul\.account_id = \$1 AND ul\.created_at >= \$2 AND ul\.created_at < \$3\s+AND \(ul\.actual_cost > 0 OR COALESCE\(ul\.account_stats_cost, ul\.total_cost\) > 0\)`).
		WithArgs(int64(91), start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "username", "email", "requests", "tokens", "account_cost", "user_cost",
		}).AddRow(int64(7), "alice", "alice@example.com", int64(12), int64(3456), 8.25, 10.5))

	rows, err := repo.GetAccountBillingUsers(context.Background(), 91, start, end)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, []int64{rows[0].UserID})
	require.Equal(t, "alice", rows[0].Username)
	require.Equal(t, 8.25, rows[0].AccountCost)
	require.Equal(t, 10.5, rows[0].UserCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountBillingUsersRetainsSuccessfulFreeUserCharge(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 8, 13, 1, 15, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	mock.ExpectQuery(`(?s)WHERE ul\.account_id = \$1.*AND \(ul\.actual_cost > 0 OR COALESCE\(ul\.account_stats_cost, ul\.total_cost\) > 0\)`).
		WithArgs(int64(91), start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "username", "email", "requests", "tokens", "account_cost", "user_cost",
		}).AddRow(int64(7), "alice", "alice@example.com", int64(1), int64(512), 0.25, 0.0))

	rows, err := repo.GetAccountBillingUsers(context.Background(), 91, start, end)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(1), rows[0].Requests)
	require.Equal(t, int64(512), rows[0].Tokens)
	require.Equal(t, 0.25, rows[0].AccountCost)
	require.Zero(t, rows[0].UserCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountBillingUsersDoesNotRequireAccountTypeLookup(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 8, 13, 1, 15, 0, 0, time.UTC)
	end := start.Add(5 * time.Hour)

	// The billing query is the first and only expected query: repositories aggregate
	// by account_id and do not join or filter on accounts.type.
	mock.ExpectQuery(`(?s)^\s*SELECT.*FROM usage_logs ul\s+LEFT JOIN users u ON u\.id = ul\.user_id\s+WHERE ul\.account_id = \$1`).
		WithArgs(int64(91), start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "username", "email", "requests", "tokens", "account_cost", "user_cost",
		}))

	rows, err := repo.GetAccountBillingUsers(context.Background(), 91, start, end)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountBillingModelsUsesRequestedModelAndCanonicalCosts(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 8, 6, 6, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 13, 6, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`(?s)SELECT\s+\$1::bigint,.*FROM users WHERE id = \$1`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email"}).AddRow(int64(7), "alice", "alice@example.com"))

	modelExpression := regexp.QuoteMeta(resolveModelDimensionExpressionWithAlias("upstream", "ul"))
	costExpression := regexp.QuoteMeta(usageLogAccountCostExpression("ul"))
	mock.ExpectQuery(`(?s)`+modelExpression+` AS model.*COALESCE\(SUM\(`+costExpression+`\), 0\) AS account_cost.*COALESCE\(SUM\(ul\.actual_cost\), 0\) AS user_cost.*WHERE ul\.account_id = \$1 AND ul\.user_id = \$2.*ul\.created_at >= \$3 AND ul\.created_at < \$4\s+AND \(ul\.actual_cost > 0 OR COALESCE\(ul\.account_stats_cost, ul\.total_cost\) > 0\).*GROUP BY `+modelExpression).
		WithArgs(int64(91), int64(7), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"model", "requests", "tokens", "account_cost", "user_cost"}).
			AddRow("gpt-5.6", int64(9), int64(2100), 6.25, 7.5))

	selected, rows, err := repo.GetAccountBillingModels(context.Background(), 91, 7, start, end)
	require.NoError(t, err)
	require.Equal(t, int64(7), selected.UserID)
	require.Len(t, rows, 1)
	require.Equal(t, "gpt-5.6", rows[0].Model)
	require.Equal(t, 6.25, rows[0].AccountCost)
	require.Equal(t, 7.5, rows[0].UserCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountBillingModelsRetainsSuccessfulFreeUserCharge(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 8, 13, 1, 15, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	mock.ExpectQuery(`(?s)SELECT\s+\$1::bigint,.*FROM users WHERE id = \$1`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email"}).AddRow(int64(7), "alice", "alice@example.com"))
	mock.ExpectQuery(`(?s)WHERE ul\.account_id = \$1 AND ul\.user_id = \$2.*AND \(ul\.actual_cost > 0 OR COALESCE\(ul\.account_stats_cost, ul\.total_cost\) > 0\)`).
		WithArgs(int64(91), int64(7), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"model", "requests", "tokens", "account_cost", "user_cost"}).
			AddRow("gpt-5.6", int64(1), int64(512), 0.25, 0.0))

	selected, rows, err := repo.GetAccountBillingModels(context.Background(), 91, 7, start, end)
	require.NoError(t, err)
	require.Equal(t, int64(7), selected.UserID)
	require.Len(t, rows, 1)
	require.Equal(t, int64(1), rows[0].Requests)
	require.Equal(t, int64(512), rows[0].Tokens)
	require.Equal(t, 0.25, rows[0].AccountCost)
	require.Zero(t, rows[0].UserCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

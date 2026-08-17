package repository

import (
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRecentActivitySortExpressionIncludesEveryActivityTimestamp(t *testing.T) {
	require.Equal(
		t,
		"GREATEST(created_at, updated_at, COALESCE(last_used_at, created_at))",
		accountRecentActivitySortExpression("created_at", "updated_at", "last_used_at"),
	)
}

func TestAccountRecentActivityOrderIsAppliedBeforeDatabasePagination(t *testing.T) {
	table := entsql.Table("accounts")
	table.SetDialect(dialect.Postgres)
	selector := entsql.Select(table.C("id")).
		From(table).
		Limit(20).
		Offset(40)
	selector.SetDialect(dialect.Postgres)
	selector.Table().SetDialect(dialect.Postgres)

	for _, applyOrder := range accountListOrder(pagination.PaginationParams{
		SortBy:    service.AccountSortRecentActivity,
		SortOrder: pagination.SortOrderDesc,
	}) {
		applyOrder(selector)
	}
	query, args := selector.Query()

	require.Empty(t, args)
	require.Contains(t, query, "GREATEST")
	require.Contains(t, query, `"accounts"."created_at"`)
	require.Contains(t, query, `"accounts"."updated_at"`)
	require.Contains(t, query, `COALESCE("accounts"."last_used_at", "accounts"."created_at")`)
	require.Contains(t, query, `"accounts"."id" DESC`)
	require.Less(t, strings.Index(query, "ORDER BY"), strings.Index(query, "LIMIT"))
	require.Less(t, strings.Index(query, "LIMIT"), strings.Index(query, "OFFSET"))
}

func TestAccountRecentActivityOrderSupportsAscendingDirection(t *testing.T) {
	table := entsql.Table("accounts")
	table.SetDialect(dialect.Postgres)
	selector := entsql.Select(table.C("id")).From(table)
	selector.SetDialect(dialect.Postgres)
	selector.Table().SetDialect(dialect.Postgres)

	for _, applyOrder := range accountListOrder(pagination.PaginationParams{
		SortBy:    service.AccountSortRecentActivity,
		SortOrder: pagination.SortOrderAsc,
	}) {
		applyOrder(selector)
	}
	query, args := selector.Query()

	require.Empty(t, args)
	require.Contains(t, query, "GREATEST")
	require.Contains(t, query, `ASC, "accounts"."id" ASC`)
}

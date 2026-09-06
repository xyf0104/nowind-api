//go:build unit

package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutionNodeUsagePredicateKeepsDeletedAccountHistory(t *testing.T) {
	conditions, args := appendExecutionNodeUsageLogWhereCondition(
		[]string{"user_id = $1"}, []any{int64(7)}, "source.example.invalid", "source.example.invalid", "ul.account_id",
	)
	require.Equal(t, []any{int64(7), "source.example.invalid", "source.example.invalid"}, args)
	require.Len(t, conditions, 2)
	require.Contains(t, conditions[1], "ul.account_id IN")
	require.Contains(t, conditions[1], "'xiass_execution_node_id'")
	require.Contains(t, conditions[1], "$2) = $3")
	require.NotContains(t, conditions[1], "deleted_at")
}

func TestExecutionNodeUsagePredicateDoesNotFilterAllOwners(t *testing.T) {
	conditions, args := appendExecutionNodeUsageLogWhereCondition(nil, nil, "", "source.example.invalid", "account_id")
	require.Empty(t, conditions)
	require.Empty(t, args)
}

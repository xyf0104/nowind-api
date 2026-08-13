package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// GetAccountBillingUsers returns per-user billing totals for one account and exact half-open range.
func (r *usageLogRepository) GetAccountBillingUsers(ctx context.Context, accountID int64, startTime, endTime time.Time) (results []usagestats.AccountBillingUser, err error) {
	query := fmt.Sprintf(`
		SELECT
			ul.user_id,
			COALESCE(u.username, '') AS username,
			COALESCE(u.email, '') AS email,
			COUNT(*) AS requests,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) AS tokens,
			COALESCE(SUM(%s), 0) AS account_cost,
			COALESCE(SUM(ul.actual_cost), 0) AS user_cost
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		WHERE ul.account_id = $1 AND ul.created_at >= $2 AND ul.created_at < $3
			AND %s
		GROUP BY ul.user_id, u.username, u.email
		ORDER BY user_cost DESC, requests DESC, ul.user_id ASC
	`, usageLogAccountCostExpression("ul"), usageLogSuccessFilterUL)

	rows, err := r.sql.QueryContext(ctx, query, accountID, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]usagestats.AccountBillingUser, 0)
	for rows.Next() {
		var row usagestats.AccountBillingUser
		if err := rows.Scan(&row.UserID, &row.Username, &row.Email, &row.Requests, &row.Tokens, &row.AccountCost, &row.UserCost); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// GetAccountBillingModels returns one user's identity and requested-model totals for an account.
func (r *usageLogRepository) GetAccountBillingModels(ctx context.Context, accountID, userID int64, startTime, endTime time.Time) (selectedUser *usagestats.AccountBillingSelectedUser, models []usagestats.AccountBillingModel, err error) {
	selectedUser = &usagestats.AccountBillingSelectedUser{}
	if scanErr := scanSingleRow(ctx, r.sql, `
		SELECT
			$1::bigint,
			COALESCE((SELECT username FROM users WHERE id = $1), ''),
			COALESCE((SELECT email FROM users WHERE id = $1), '')
	`, []any{userID}, &selectedUser.UserID, &selectedUser.Username, &selectedUser.Email); scanErr != nil {
		return nil, nil, scanErr
	}

	modelExpr := resolveModelDimensionExpressionWithAlias(usagestats.ModelSourceUpstream, "ul")
	query := fmt.Sprintf(`
		SELECT
			%s AS model,
			COUNT(*) AS requests,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) AS tokens,
			COALESCE(SUM(%s), 0) AS account_cost,
			COALESCE(SUM(ul.actual_cost), 0) AS user_cost
		FROM usage_logs ul
		WHERE ul.account_id = $1 AND ul.user_id = $2
			AND ul.created_at >= $3 AND ul.created_at < $4
			AND %s
		GROUP BY %s
		ORDER BY user_cost DESC, requests DESC, model ASC
	`, modelExpr, usageLogAccountCostExpression("ul"), usageLogSuccessFilterUL, modelExpr)

	rows, err := r.sql.QueryContext(ctx, query, accountID, userID, startTime, endTime)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			selectedUser = nil
			models = nil
		}
	}()

	models = make([]usagestats.AccountBillingModel, 0)
	for rows.Next() {
		var row usagestats.AccountBillingModel
		if err := rows.Scan(&row.Model, &row.Requests, &row.Tokens, &row.AccountCost, &row.UserCost); err != nil {
			return nil, nil, err
		}
		models = append(models, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return selectedUser, models, nil
}

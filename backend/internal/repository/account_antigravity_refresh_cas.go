package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// UpdateAntigravityOAuthCredentialsIfUnchanged persists a provider-issued
// replacement only while the complete credential document and proxy still
// match the upstream attempt. The scheduler outbox write is part of the same
// statement, so a successful credential rotation is never published partially.
func (r *accountRepository) UpdateAntigravityOAuthCredentialsIfUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	expectedProxyUpdatedAt *time.Time,
	credentials map[string]any,
) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}
	expectedJSON, err := json.Marshal(normalizeJSONMap(expectedCredentials))
	if err != nil {
		return false, err
	}
	credentialsJSON, err := json.Marshal(normalizeJSONMap(credentials))
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
		UPDATE accounts AS a
		SET credentials = $1::jsonb,
			updated_at = NOW()
		WHERE a.id = $2
			AND a.deleted_at IS NULL
			AND a.platform = $3
			AND a.type = $4
				AND a.credentials = $5::jsonb
				AND a.proxy_id IS NOT DISTINCT FROM $6
				AND (($6::bigint IS NULL AND $7::timestamptz IS NULL) OR EXISTS (
					SELECT 1 FROM proxies p
					WHERE p.id = a.proxy_id AND p.deleted_at IS NULL
						AND p.updated_at IS NOT DISTINCT FROM $7
				))
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $8, updated.id, NULL, NULL FROM updated
	`,
		string(credentialsJSON),
		id,
		service.PlatformAntigravity,
		service.AccountTypeOAuth,
		string(expectedJSON),
		expectedProxyID,
		expectedProxyUpdatedAt,
		service.SchedulerOutboxEventAccountChanged,
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, id)
	return true, nil
}

// UpdateAntigravityOAuthCredentialsAndPrivacyIfUnchanged atomically replaces
// an interactive OAuth identity and clears the prior privacy marker. Keeping
// both writes in one CAS prevents an older reauthorization from resetting a
// newer authorization's privacy state.
func (r *accountRepository) UpdateAntigravityOAuthCredentialsAndPrivacyIfUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	expectedProxyUpdatedAt *time.Time,
	credentials map[string]any,
) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}
	expectedJSON, err := json.Marshal(normalizeJSONMap(expectedCredentials))
	if err != nil {
		return false, err
	}
	credentialsJSON, err := json.Marshal(normalizeJSONMap(credentials))
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
		UPDATE accounts AS a
		SET credentials = $1::jsonb,
			extra = COALESCE(a.extra, '{}'::jsonb) || jsonb_build_object('privacy_mode', $2::text),
			updated_at = NOW()
		WHERE a.id = $3
			AND a.deleted_at IS NULL
			AND a.platform = $4
			AND a.type = $5
			AND a.credentials = $6::jsonb
			AND a.proxy_id IS NOT DISTINCT FROM $7
			AND (($7::bigint IS NULL AND $8::timestamptz IS NULL) OR EXISTS (
				SELECT 1 FROM proxies p
				WHERE p.id = a.proxy_id AND p.deleted_at IS NULL
					AND p.updated_at IS NOT DISTINCT FROM $8
			))
		RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $9, updated.id, NULL, NULL FROM updated
	`,
		string(credentialsJSON),
		service.AntigravityPrivacyFailed,
		id,
		service.PlatformAntigravity,
		service.AccountTypeOAuth,
		string(expectedJSON),
		expectedProxyID,
		expectedProxyUpdatedAt,
		service.SchedulerOutboxEventAccountChanged,
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, id)
	return true, nil
}

func (r *accountRepository) SetAntigravityOAuthRefreshErrorIfCredentialsUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	expectedProxyUpdatedAt *time.Time,
	errorMsg string,
) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}
	expectedJSON, err := json.Marshal(normalizeJSONMap(expectedCredentials))
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
		UPDATE accounts AS a
		SET status = $1,
			error_message = $2,
			schedulable = FALSE,
			updated_at = NOW()
		WHERE a.id = $3
			AND a.deleted_at IS NULL
			AND a.platform = $4
			AND a.type = $5
			AND a.status = $6
				AND a.credentials = $7::jsonb
				AND a.proxy_id IS NOT DISTINCT FROM $8
				AND (($8::bigint IS NULL AND $9::timestamptz IS NULL) OR EXISTS (
					SELECT 1 FROM proxies p
					WHERE p.id = a.proxy_id AND p.deleted_at IS NULL
						AND p.updated_at IS NOT DISTINCT FROM $9
				))
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $10, updated.id, NULL, NULL FROM updated
	`,
		service.StatusError,
		errorMsg,
		id,
		service.PlatformAntigravity,
		service.AccountTypeOAuth,
		service.StatusActive,
		string(expectedJSON),
		expectedProxyID,
		expectedProxyUpdatedAt,
		service.SchedulerOutboxEventAccountChanged,
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, id)
	return true, nil
}

func (r *accountRepository) SetAntigravityOAuthRefreshTempUnschedulableIfCredentialsUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	expectedProxyUpdatedAt *time.Time,
	until time.Time,
	reason string,
) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}
	expectedJSON, err := json.Marshal(normalizeJSONMap(expectedCredentials))
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
		UPDATE accounts AS a
		SET temp_unschedulable_until = $1,
			temp_unschedulable_reason = $2,
			updated_at = NOW()
		WHERE a.id = $3
			AND a.deleted_at IS NULL
			AND a.platform = $4
			AND a.type = $5
			AND a.status = $6
				AND a.credentials = $7::jsonb
				AND a.proxy_id IS NOT DISTINCT FROM $8
				AND (($8::bigint IS NULL AND $9::timestamptz IS NULL) OR EXISTS (
					SELECT 1 FROM proxies p
					WHERE p.id = a.proxy_id AND p.deleted_at IS NULL
						AND p.updated_at IS NOT DISTINCT FROM $9
				))
				AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until < $1)
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $10, updated.id, NULL, NULL FROM updated
	`,
		until,
		reason,
		id,
		service.PlatformAntigravity,
		service.AccountTypeOAuth,
		service.StatusActive,
		string(expectedJSON),
		expectedProxyID,
		expectedProxyUpdatedAt,
		service.SchedulerOutboxEventAccountChanged,
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, id)
	return true, nil
}

func (r *accountRepository) UpdateAntigravityOAuthRefreshExtraIfCredentialsUnchanged(
	ctx context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	expectedProxyUpdatedAt *time.Time,
	updates map[string]any,
) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}
	expectedJSON, err := json.Marshal(normalizeJSONMap(expectedCredentials))
	if err != nil {
		return false, err
	}
	updatesJSON, err := json.Marshal(normalizeJSONMap(updates))
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
		UPDATE accounts AS a
		SET extra = COALESCE(a.extra, '{}'::jsonb) || $1::jsonb,
			updated_at = NOW()
		WHERE a.id = $2
			AND a.deleted_at IS NULL
			AND a.platform = $3
			AND a.type = $4
				AND a.credentials = $5::jsonb
				AND a.proxy_id IS NOT DISTINCT FROM $6
				AND (($6::bigint IS NULL AND $7::timestamptz IS NULL) OR EXISTS (
					SELECT 1 FROM proxies p
					WHERE p.id = a.proxy_id AND p.deleted_at IS NULL
						AND p.updated_at IS NOT DISTINCT FROM $7
				))
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $8, updated.id, NULL, NULL FROM updated
	`,
		string(updatesJSON),
		id,
		service.PlatformAntigravity,
		service.AccountTypeOAuth,
		string(expectedJSON),
		expectedProxyID,
		expectedProxyUpdatedAt,
		service.SchedulerOutboxEventAccountChanged,
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, id)
	return true, nil
}

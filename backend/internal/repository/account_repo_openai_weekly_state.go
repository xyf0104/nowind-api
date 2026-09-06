package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	openAIWeeklyStateEpochKey        = "codex_7d_estimate_epoch"
	openAIWeeklyStateBaselineKey     = "codex_7d_estimate_baseline"
	openAIWeeklyStateUsageUpdatedKey = "codex_usage_updated_at"
)

var _ service.OpenAIWeeklyStateRepository = (*accountRepository)(nil)

// -> returns SQL NULL for a missing key and JSON null for a present null value.
// IS NOT DISTINCT FROM preserves that distinction without string/time coercion.
const compareAndSwapOpenAIWeeklyStateQuery = `
	UPDATE accounts
	SET extra = CASE WHEN extra IS NULL OR extra = 'null'::jsonb
		THEN '{}'::jsonb ELSE extra END || $1::jsonb
	WHERE id = $2 AND deleted_at IS NULL
		AND platform = $3 AND type = $4
		AND credentials = $5::jsonb
		AND proxy_id IS NOT DISTINCT FROM $6
		AND (extra IS NULL OR jsonb_typeof(extra) IN ('object', 'null'))
		AND (extra -> 'codex_7d_estimate_epoch') IS NOT DISTINCT FROM ($7::jsonb -> 'codex_7d_estimate_epoch')
		AND (extra -> 'codex_7d_estimate_baseline') IS NOT DISTINCT FROM ($7::jsonb -> 'codex_7d_estimate_baseline')
		AND (extra -> 'codex_usage_updated_at') IS NOT DISTINCT FROM ($7::jsonb -> 'codex_usage_updated_at')
		AND (extra -> 'codex_7d_estimate_revision') IS NOT DISTINCT FROM ($7::jsonb -> 'codex_7d_estimate_revision')
		AND xiass_openai_weekly_state_write_allowed(extra,
			CASE WHEN extra IS NULL OR extra = 'null'::jsonb THEN '{}'::jsonb ELSE extra END || $1::jsonb,
			credentials)
`

func (r *accountRepository) CompareAndSwapOpenAIWeeklyState(ctx context.Context, expected *service.Account, updates map[string]any) (bool, error) {
	if expected == nil || expected.ID <= 0 || expected.Platform != service.PlatformOpenAI || expected.Type != service.AccountTypeOAuth {
		return false, fmt.Errorf("%w: expected account must be an existing OpenAI OAuth account", service.ErrOpenAIWeeklyStateInvalidInput)
	}
	if len(updates) == 0 {
		return false, fmt.Errorf("%w: empty patch", service.ErrOpenAIWeeklyStateInvalidInput)
	}
	for key := range updates {
		if key != openAIWeeklyStateEpochKey && key != openAIWeeklyStateBaselineKey && key != service.OpenAIWeeklyStateRevisionKey {
			return false, fmt.Errorf("%w: patch contains a non-state key", service.ErrOpenAIWeeklyStateInvalidInput)
		}
	}
	updates, err := service.PrepareOpenAIWeeklyStateUpdate(expected.Extra, updates)
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(updates)
	if err != nil {
		return false, fmt.Errorf("%w: patch is not JSON-encodable", service.ErrOpenAIWeeklyStateInvalidInput)
	}
	credentials, err := json.Marshal(expected.Credentials)
	if err != nil {
		// Do not include credential values or custom marshaler error text.
		return false, fmt.Errorf("%w: expected credentials are not JSON-encodable", service.ErrOpenAIWeeklyStateInvalidInput)
	}
	state := make(map[string]any, 4)
	for _, key := range []string{openAIWeeklyStateEpochKey, openAIWeeklyStateBaselineKey, openAIWeeklyStateUsageUpdatedKey, service.OpenAIWeeklyStateRevisionKey} {
		if value, present := expected.Extra[key]; present {
			state[key] = value
		}
	}
	snapshot, err := json.Marshal(state)
	if err != nil {
		return false, fmt.Errorf("%w: expected state is not JSON-encodable", service.ErrOpenAIWeeklyStateInvalidInput)
	}

	var exec sqlExecutor
	if tx := dbent.TxFromContext(ctx); tx != nil {
		exec = tx.Client()
	} else if r != nil {
		exec = r.sql
	}
	if exec == nil {
		return false, errors.New("OpenAI weekly state SQL executor is not configured")
	}
	// One conditional UPDATE rechecks the snapshot after a concurrent row writer
	// completes. Deliberately bypass generic Extra updates and scheduler side effects.
	result, err := exec.ExecContext(ctx, compareAndSwapOpenAIWeeklyStateQuery,
		string(payload), expected.ID, expected.Platform, expected.Type, string(credentials), expected.ProxyID, string(snapshot))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

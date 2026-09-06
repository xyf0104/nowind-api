//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func boundCodexPGAccount(t *testing.T) *service.Account {
	t.Helper()
	a := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "synthetic-old-access", "refresh_token": "synthetic-old-refresh", "_token_version": 1},
		Extra: map[string]any{"codex_usage_updated_at": "2026-09-06T00:00:00Z", "codex_7d_used_percent": 10.0,
			"codex_7d_estimate_baseline": map[string]any{"opaque": "keep"}, "unrelated": "keep"}}
	credentials, err := json.Marshal(a.Credentials)
	require.NoError(t, err)
	extra, err := json.Marshal(a.Extra)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts (name, platform, type, credentials, extra)
		VALUES ('bound-codex-snapshot-test', $1, $2, $3::jsonb, $4::jsonb) RETURNING id`,
		a.Platform, a.Type, string(credentials), string(extra)).Scan(&a.ID))
	return a
}

func boundCodexPGRecord(t *testing.T, id int64) map[string]any {
	t.Helper()
	var raw []byte
	require.NoError(t, integrationDB.QueryRow(`SELECT to_jsonb(a) FROM accounts a WHERE id = $1`, id).Scan(&raw))
	var record map[string]any
	require.NoError(t, json.Unmarshal(raw, &record))
	return record
}

func boundCodexPGRepo() *accountRepository {
	return &accountRepository{client: integrationEntClient, sql: integrationDB}
}

func TestBoundCodexSnapshotPGLateOldTokenLosesToReauthorization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	oldRequest := boundCodexPGAccount(t)
	newRequest := *oldRequest
	newRequest.Credentials = map[string]any{"access_token": "synthetic-new-access", "refresh_token": "synthetic-new-refresh", "_token_version": 2}
	credentials, err := json.Marshal(newRequest.Credentials)
	require.NoError(t, err)
	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Client().ExecContext(ctx, `UPDATE accounts SET credentials = $1::jsonb WHERE id = $2`, string(credentials), oldRequest.ID)
	require.NoError(t, err)
	applied, err := boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(dbent.NewTxContext(ctx, tx), &newRequest,
		map[string]any{"codex_usage_updated_at": "2026-09-06T00:00:01Z", "codex_7d_used_percent": 20.0})
	require.NoError(t, err)
	require.True(t, applied)

	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	var pid int
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	type outcome struct {
		applied bool
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		applied, err := (&accountRepository{sql: conn}).UpdateOpenAICodexSnapshotIfIdentityMatches(ctx, oldRequest,
			map[string]any{"codex_usage_updated_at": "2026-09-06T00:00:09Z", "codex_7d_used_percent": 99.0})
		done <- outcome{applied, err}
	}()
	// Confirm the old request's UPDATE is actually blocked behind reauth, not
	// merely scheduled after it. PostgreSQL must recheck identity on wakeup.
	require.Eventually(t, func() bool {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting)
		return err == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit())
	result := <-done
	require.NoError(t, result.err)
	require.False(t, result.applied, "a newer observation time cannot authorize the old token")
	record := boundCodexPGRecord(t, oldRequest.ID)
	require.Equal(t, "synthetic-new-access", record["credentials"].(map[string]any)["access_token"])
	extra := record["extra"].(map[string]any)
	require.Equal(t, 20.0, extra["codex_7d_used_percent"])
	require.Equal(t, "2026-09-06T00:00:01Z", extra["codex_usage_updated_at"])
	require.Equal(t, map[string]any{"opaque": "keep"}, extra["codex_7d_estimate_baseline"])
	require.Equal(t, "keep", extra["unrelated"])
}

func TestBoundCodexSnapshotPGExactIdentityGuards(t *testing.T) {
	proxy := mustCreateProxy(t, testEntClient(t), &service.Proxy{Name: "bound-codex-proxy"})
	cleanupOpenAIQuotaTestProxy(t, proxy.ID)
	for _, tc := range []struct{ name, query string }{
		{"access token", `UPDATE accounts SET credentials = credentials || '{"access_token":"synthetic-new"}'::jsonb WHERE id = $1`},
		{"refresh token", `UPDATE accounts SET credentials = credentials || '{"refresh_token":"synthetic-new"}'::jsonb WHERE id = $1`},
		{"token version", `UPDATE accounts SET credentials = credentials || '{"_token_version":2}'::jsonb WHERE id = $1`},
		{"explicit null", `UPDATE accounts SET credentials = credentials || '{"new_identity_field":null}'::jsonb WHERE id = $1`},
		{"nested credentials", `UPDATE accounts SET credentials = credentials || '{"identity":{"tenant":"new"}}'::jsonb WHERE id = $1`},
		{"proxy", `UPDATE accounts SET proxy_id = $2 WHERE id = $1`},
		{"platform", `UPDATE accounts SET platform = 'anthropic' WHERE id = $1`},
		{"type", `UPDATE accounts SET type = 'apikey' WHERE id = $1`},
		{"deleted", `UPDATE accounts SET deleted_at = NOW() WHERE id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := boundCodexPGAccount(t)
			args := []any{a.ID}
			if tc.name == "proxy" {
				args = append(args, proxy.ID)
			}
			_, err := integrationDB.Exec(tc.query, args...)
			require.NoError(t, err)
			before := boundCodexPGRecord(t, a.ID)
			applied, err := boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), a,
				map[string]any{"codex_usage_updated_at": "2026-09-06T12:00:00Z", "codex_7d_used_percent": 99.0})
			require.NoError(t, err)
			require.False(t, applied)
			require.Equal(t, before, boundCodexPGRecord(t, a.ID))
		})
	}
	a := boundCodexPGAccount(t)
	a.ProxyID = &proxy.ID
	applied, err := boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), a,
		map[string]any{"codex_usage_updated_at": "2026-09-06T12:00:00Z"})
	require.NoError(t, err)
	require.False(t, applied, "non-null request proxy must not match current SQL NULL")
}

func TestBoundCodexSnapshotPGOrderingAndRuntimeFieldsOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stored  any
		applied bool
	}{
		{"missing", nil, true},
		{"invalid legacy", "invalid-time", true},
		{"malformed calendar", "2026-99-99T00:00:00Z", true},
		{"older", "2026-09-06T00:00:00Z", true},
		{"equal nanos", "2026-09-06T00:00:01.123456789Z", true},
		{"newer", "2026-09-06T00:00:01.123457Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := boundCodexPGAccount(t)
			if tc.stored == nil {
				_, err := integrationDB.Exec(`UPDATE accounts SET extra = extra - 'codex_usage_updated_at' WHERE id = $1`, a.ID)
				require.NoError(t, err)
			} else {
				_, err := integrationDB.Exec(`UPDATE accounts SET extra = extra || jsonb_build_object('codex_usage_updated_at', $2::text) WHERE id = $1`, a.ID, tc.stored)
				require.NoError(t, err)
			}
			before := boundCodexPGRecord(t, a.ID)
			applied, err := boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), a,
				map[string]any{"codex_usage_updated_at": "2026-09-06T08:00:01.123456999+08:00", "codex_5h_used_percent": nil, "codex_7d_used_percent": 20.0})
			require.NoError(t, err)
			require.Equal(t, tc.applied, applied)
			after := boundCodexPGRecord(t, a.ID)
			if !applied {
				require.Equal(t, before, after)
				return
			}
			extra := after["extra"].(map[string]any)
			require.Equal(t, "2026-09-06T00:00:01.123456Z", extra["codex_usage_updated_at"])
			require.Equal(t, 20.0, extra["codex_7d_used_percent"])
			require.Contains(t, extra, "codex_5h_used_percent")
			require.Nil(t, extra["codex_5h_used_percent"])
			require.Equal(t, before["extra"].(map[string]any)["codex_7d_estimate_baseline"], extra["codex_7d_estimate_baseline"])
			applied, err = boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), a,
				map[string]any{"codex_usage_updated_at": "2026-09-06T12:00:00Z", "codex_7d_estimate_baseline": nil})
			require.ErrorContains(t, err, "non-runtime quota field")
			require.False(t, applied)
			require.Equal(t, after, boundCodexPGRecord(t, a.ID))
		})
	}
}

func TestBoundCodexSnapshotPGShadowParentTokenRaceFailsClosed(t *testing.T) {
	parent := boundCodexPGAccount(t)
	shadow := boundCodexPGAccount(t)
	_, err := integrationDB.Exec(`UPDATE accounts SET parent_account_id = $2, quota_dimension = 'spark', credentials = '{}'::jsonb WHERE id = $1`, shadow.ID, parent.ID)
	require.NoError(t, err)
	shadow.ParentAccountID = &parent.ID
	shadow.QuotaDimension = service.QuotaDimensionSpark
	shadow.Credentials = map[string]any{}
	_, err = integrationDB.Exec(`UPDATE accounts SET credentials = credentials || '{"access_token":"synthetic-new-parent"}'::jsonb WHERE id = $1`, parent.ID)
	require.NoError(t, err)
	before := boundCodexPGRecord(t, shadow.ID)
	patch := map[string]any{"codex_usage_updated_at": "2026-09-06T12:00:00Z", "codex_7d_used_percent": 99.0}
	applied, err := boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), shadow, patch)
	require.ErrorContains(t, err, "captured parent request identity")
	require.False(t, applied)
	// A stale global request snapshot must also fail if the row became a shadow.
	shadow.ParentAccountID, shadow.QuotaDimension = nil, service.QuotaDimensionGlobal
	applied, err = boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), shadow, patch)
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, before, boundCodexPGRecord(t, shadow.ID))
}

func boundQuotaPGShadow(t *testing.T, parent *service.Account) *service.Account {
	t.Helper()
	shadow := boundCodexPGAccount(t)
	_, err := integrationDB.Exec(`UPDATE accounts SET parent_account_id = $2, quota_dimension = 'spark', credentials = '{}'::jsonb WHERE id = $1`, shadow.ID, parent.ID)
	require.NoError(t, err)
	shadow.ParentAccountID, shadow.QuotaDimension, shadow.Credentials = &parent.ID, service.QuotaDimensionSpark, map[string]any{}
	return shadow
}

func boundQuotaPGPatch(at time.Time) map[string]any {
	return map[string]any{
		"codex_usage_updated_at": at.Format(time.RFC3339Nano), "codex_7d_used_percent": 34.0,
		"codex_reset_credit_snapshot":            map[string]any{"available_count": 0},
		"codex_reset_credit_snapshot_updated_at": at.Format(time.RFC3339Nano),
	}
}

func TestBoundQuotaPGShadowParentReauthWaitsThenRejectsOldQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	parent := boundCodexPGAccount(t)
	shadow := boundQuotaPGShadow(t, parent)
	at := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	applied, err := boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, shadow, parent, at, boundQuotaPGPatch(at))
	require.NoError(t, err)
	require.True(t, applied, "unchanged parent permits Spark refresh")
	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Client().ExecContext(ctx, `UPDATE accounts SET credentials = credentials || '{"access_token":"synthetic-parent-reauth","_token_version":2}'::jsonb WHERE id = $1`, parent.ID)
	require.NoError(t, err)
	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	var pid int
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	type outcome struct {
		applied bool
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		late := at.Add(time.Hour)
		applied, err := (&accountRepository{sql: conn}).UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, shadow, parent, late, boundQuotaPGPatch(late))
		done <- outcome{applied, err}
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting)
		return err == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit())
	result := <-done
	require.NoError(t, result.err)
	require.False(t, result.applied)
	extra := boundCodexPGRecord(t, shadow.ID)["extra"].(map[string]any)
	require.Equal(t, at.Format(time.RFC3339Nano), extra["codex_usage_updated_at"])
	require.Equal(t, at.Format(time.RFC3339Nano), extra["codex_reset_credit_snapshot_updated_at"])
	require.Equal(t, map[string]any{"opaque": "keep"}, extra["codex_7d_estimate_baseline"])
	// Credits-only writes from the old query are subject to the same parent CAS.
	patch := boundQuotaPGPatch(at.Add(2 * time.Hour))
	delete(patch, "codex_usage_updated_at")
	delete(patch, "codex_7d_used_percent")
	applied, err = boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, shadow, parent, at.Add(2*time.Hour), patch)
	require.NoError(t, err)
	require.False(t, applied)
}

func TestBoundQuotaPGParentAndTargetIdentityGuards(t *testing.T) {
	proxy := mustCreateProxy(t, testEntClient(t), &service.Proxy{Name: "bound-quota-proxy"})
	cleanupOpenAIQuotaTestProxy(t, proxy.ID)
	for _, tc := range []struct {
		name, query string
		parent      bool
	}{
		{"parent proxy", `UPDATE accounts SET proxy_id = $2 WHERE id = $1`, true},
		{"parent credentials", `UPDATE accounts SET credentials = credentials || '{"nested":{"new":true}}'::jsonb WHERE id = $1`, true},
		{"parent platform", `UPDATE accounts SET platform = 'anthropic' WHERE id = $1`, true},
		{"parent type", `UPDATE accounts SET type = 'apikey' WHERE id = $1`, true},
		{"parent deleted", `UPDATE accounts SET deleted_at = NOW() WHERE id = $1`, true},
		{"target proxy", `UPDATE accounts SET proxy_id = $2 WHERE id = $1`, false},
		{"target credentials", `UPDATE accounts SET credentials = '{"unexpected":null}'::jsonb WHERE id = $1`, false},
		{"target parent", `UPDATE accounts SET parent_account_id = $2 WHERE id = $1`, false},
		{"target deleted", `UPDATE accounts SET deleted_at = NOW() WHERE id = $1`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := boundCodexPGAccount(t)
			shadow := boundQuotaPGShadow(t, parent)
			id := shadow.ID
			if tc.parent {
				id = parent.ID
			}
			args := []any{id}
			if tc.name == "parent proxy" || tc.name == "target proxy" {
				args = append(args, proxy.ID)
			}
			if tc.name == "target parent" {
				args = append(args, boundCodexPGAccount(t).ID)
			}
			_, err := integrationDB.Exec(tc.query, args...)
			require.NoError(t, err)
			before := boundCodexPGRecord(t, shadow.ID)
			at := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
			applied, err := boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(context.Background(), shadow, parent, at, boundQuotaPGPatch(at))
			require.NoError(t, err)
			require.False(t, applied)
			require.Equal(t, before, boundCodexPGRecord(t, shadow.ID))
		})
	}
}

func TestBoundQuotaPGNormalPostResetAndCreditOrdering(t *testing.T) {
	ctx := context.Background()
	account := boundCodexPGAccount(t)
	at := time.Date(2026, 9, 6, 1, 2, 3, 123456000, time.UTC)
	applied, err := boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, account, account, at, boundQuotaPGPatch(at))
	require.NoError(t, err)
	require.True(t, applied)
	creditsOnly := boundQuotaPGPatch(at.Add(time.Second))
	delete(creditsOnly, "codex_usage_updated_at")
	delete(creditsOnly, "codex_7d_used_percent")
	applied, err = boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, account, account, at.Add(time.Second), creditsOnly)
	require.NoError(t, err)
	require.True(t, applied)
	before := boundCodexPGRecord(t, account.ID)
	require.Equal(t, at.Format(time.RFC3339Nano), before["extra"].(map[string]any)["codex_usage_updated_at"])
	// A later credits-only refresh prevents an older combined write from
	// partially rolling back either quota or credits.
	applied, err = boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, account, account, at, boundQuotaPGPatch(at))
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, before, boundCodexPGRecord(t, account.ID))
}

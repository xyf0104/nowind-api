//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"maps"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func weeklyFencePGState(identity string, at time.Time) map[string]any {
	return map[string]any{
		"version": 15, "identity": identity, "observed_at": at.Format(time.RFC3339Nano),
		"reset_at": at.Add(3 * 24 * time.Hour).Format(time.RFC3339Nano),
		"mode":     "join_average", "baseline_source": "verified_history_inception",
		"baseline_percent": 20.0, "baseline_cost": 0.0,
		"snapshot_percent": 21.0, "snapshot_cost": 20.0, "percent_bucket": 21,
		"completed_percent": 21.0, "completed_cost": 20.0,
		"has_weekly_estimate": true, "awaiting_interval": false, "terminal": false, "estimate_usd": 2000.0,
		"join_evidence": map[string]any{"kind": "synthetic_verified_inception", "percent": 20.0, "cost": 0.0},
	}
}

func weeklyFencePGAccount(t *testing.T) *service.Account {
	t.Helper()
	identity := "synthetic-fenced-principal"
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Add(2 * time.Nanosecond)
	a := weeklyStatePGAccount(t, map[string]any{"chatgpt_account_id": identity, "chatgpt_user_id": "synthetic-user", "access_token": "synthetic-original"},
		map[string]any{openAIWeeklyStateBaselineKey: weeklyFencePGState(identity, at), openAIWeeklyStateEpochKey: "synthetic-epoch",
			openAIWeeklyStateUsageUpdatedKey: at.Format(time.RFC3339Nano), "codex_7d_used_percent": 21.0, "ordinary": "keep"})
	a.Extra = weeklyStatePGRecord(t, a.ID)["extra"].(map[string]any)
	require.Equal(t, 1.0, a.Extra[service.OpenAIWeeklyStateRevisionKey])
	return a
}

func weeklyFencePGReload(t *testing.T, a *service.Account) {
	t.Helper()
	a.Extra = weeklyStatePGRecord(t, a.ID)["extra"].(map[string]any)
}

func weeklyFencePGWrite(t *testing.T, a *service.Account, state map[string]any) bool {
	t.Helper()
	applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(context.Background(), a,
		map[string]any{openAIWeeklyStateBaselineKey: state})
	require.NoError(t, err)
	return applied
}

func TestOpenAIWeeklyFencePGLegacyWritersCannotReplaceV15(t *testing.T) {
	for _, mode := range []string{"v14 unconditional", "v15 stale full form", "missing state full form", "null state", "forged revision", "invalid revision"} {
		t.Run(mode, func(t *testing.T) {
			a := weeklyFencePGAccount(t)
			stale := maps.Clone(a.Extra)
			repaired := maps.Clone(a.Extra[openAIWeeklyStateBaselineKey].(map[string]any))
			repaired["baseline_percent"] = 11.0
			repaired["join_evidence"] = map[string]any{"kind": "synthetic_repair", "percent": 11.0, "cost": 0.0}
			require.True(t, weeklyFencePGWrite(t, a, repaired))
			accepted := weeklyStatePGRecord(t, a.ID)["extra"].(map[string]any)
			patch := stale
			query := `UPDATE accounts SET extra = $1::jsonb, name = 'ordinary edit survived' WHERE id = $2`
			switch mode {
			case "v14 unconditional":
				legacy := maps.Clone(repaired)
				legacy["version"], legacy["observed_at"] = 14, time.Now().Add(time.Hour).Format(time.RFC3339Nano)
				patch = map[string]any{openAIWeeklyStateBaselineKey: legacy, openAIWeeklyStateEpochKey: "old-writer-epoch"}
				query = `UPDATE accounts SET extra = extra || $1::jsonb, name = 'ordinary edit survived' WHERE id = $2`
			case "missing state full form":
				patch = map[string]any{}
			case "null state":
				patch[openAIWeeklyStateBaselineKey] = nil
			case "forged revision":
				patch[service.OpenAIWeeklyStateRevisionKey] = 1000
			case "invalid revision":
				patch[service.OpenAIWeeklyStateRevisionKey] = nil
			}
			patch["ordinary"] = "edited"
			encoded, err := json.Marshal(patch)
			require.NoError(t, err)
			_, err = integrationDB.Exec(query, string(encoded), a.ID)
			require.NoError(t, err)
			row := weeklyStatePGRecord(t, a.ID)
			got := row["extra"].(map[string]any)
			for key, value := range accepted {
				if service.IsOpenAIQuotaRuntimeExtraKey(key) {
					require.Equal(t, value, got[key], key)
				}
			}
			require.Equal(t, "edited", got["ordinary"])
			require.Equal(t, "ordinary edit survived", row["name"])
		})
	}
}

func TestOpenAIWeeklyFencePGSameObservationRepairRace(t *testing.T) {
	a := weeklyFencePGAccount(t)
	const writers = 16
	start := make(chan struct{})
	type outcome struct {
		applied bool
		err     error
	}
	results := make(chan outcome, writers)
	for i := 0; i < writers; i++ {
		go func(index int) {
			state := maps.Clone(a.Extra[openAIWeeklyStateBaselineKey].(map[string]any))
			state["join_evidence"] = map[string]any{"synthetic_candidate": index}
			<-start
			applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(context.Background(), a,
				map[string]any{openAIWeeklyStateBaselineKey: state})
			results <- outcome{applied, err}
		}(i)
	}
	close(start)
	wins := 0
	for i := 0; i < writers; i++ {
		result := <-results
		require.NoError(t, result.err)
		if result.applied {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	got := weeklyStatePGRecord(t, a.ID)["extra"].(map[string]any)
	require.Equal(t, 2.0, got[service.OpenAIWeeklyStateRevisionKey])
	require.Equal(t, a.Extra[openAIWeeklyStateBaselineKey].(map[string]any)["observed_at"],
		got[openAIWeeklyStateBaselineKey].(map[string]any)["observed_at"])
}

func TestOpenAIWeeklyFencePGSameStateFormCannotRollbackRawQuota(t *testing.T) {
	ctx := context.Background()
	a := weeklyFencePGAccount(t)
	stale := maps.Clone(a.Extra)
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	patch := map[string]any{
		"codex_usage_updated_at": at.Format(time.RFC3339Nano), "codex_7d_used_percent": 31.0,
		"codex_reset_credit_snapshot_updated_at": at.Format(time.RFC3339Nano),
		"codex_reset_credit_snapshot":            map[string]any{"remaining": 7.0},
	}
	applied, err := boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, a, a, at, patch)
	require.NoError(t, err)
	require.True(t, applied)
	accepted := weeklyStatePGRecord(t, a.ID)["extra"].(map[string]any)
	for _, key := range []string{openAIWeeklyStateBaselineKey, openAIWeeklyStateEpochKey, service.OpenAIWeeklyStateRevisionKey} {
		require.Equal(t, stale[key], accepted[key], "the reported gap has identical estimator state")
	}
	stale["ordinary"] = "old form edit accepted"
	encoded, err := json.Marshal(stale)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE accounts SET extra = $1::jsonb, name = 'old form raw snapshot' WHERE id = $2`, string(encoded), a.ID)
	require.NoError(t, err)
	row := weeklyStatePGRecord(t, a.ID)
	got := row["extra"].(map[string]any)
	for key, value := range accepted {
		if service.IsOpenAIQuotaRuntimeExtraKey(key) {
			require.Equal(t, value, got[key], key)
		}
	}
	require.Equal(t, "old form edit accepted", got["ordinary"])
	require.Equal(t, "old form raw snapshot", row["name"])

	// New bound writes still advance after the stale form, without changing the estimator revision.
	at = at.Add(time.Second)
	patch["codex_usage_updated_at"], patch["codex_7d_used_percent"] = at.Format(time.RFC3339Nano), 32.0
	patch["codex_reset_credit_snapshot_updated_at"] = at.Format(time.RFC3339Nano)
	patch["codex_reset_credit_snapshot"] = map[string]any{"remaining": 6.0}
	applied, err = boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, a, a, at, patch)
	require.NoError(t, err)
	require.True(t, applied)
	weeklyFencePGReload(t, a)
	require.Equal(t, 32.0, a.Extra["codex_7d_used_percent"])
	require.Equal(t, 1.0, a.Extra[service.OpenAIWeeklyStateRevisionKey])
}

func TestOpenAIWeeklyFencePGRawSnapshotCredentialAndNanosecondGuards(t *testing.T) {
	ctx := context.Background()
	a := weeklyFencePGAccount(t)
	at, err := time.Parse(time.RFC3339Nano, a.Extra[openAIWeeklyStateUsageUpdatedKey].(string))
	require.NoError(t, err)
	applied, err := boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(ctx, a,
		map[string]any{"codex_usage_updated_at": at.Add(-time.Nanosecond).Format(time.RFC3339Nano), "codex_7d_used_percent": 99.0})
	require.NoError(t, err)
	require.False(t, applied, "a trigger-preserved raw write must not report applied=true")
	form := maps.Clone(a.Extra)
	form["codex_usage_updated_at"], form["codex_7d_used_percent"] = at.Add(time.Minute).Format(time.RFC3339Nano), 80.0
	encoded, err := json.Marshal(form)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE accounts SET credentials = credentials || '{"access_token":"synthetic-rotated"}'::jsonb,
		extra = $1::jsonb, name = 'credential edit accepted' WHERE id = $2`, string(encoded), a.ID)
	require.NoError(t, err)
	row := weeklyStatePGRecord(t, a.ID)
	require.Equal(t, "credential edit accepted", row["name"])
	require.Equal(t, a.Extra[openAIWeeklyStateUsageUpdatedKey], row["extra"].(map[string]any)[openAIWeeklyStateUsageUpdatedKey])
	require.Equal(t, 21.0, row["extra"].(map[string]any)["codex_7d_used_percent"])
	patch := map[string]any{"codex_usage_updated_at": at.Add(2 * time.Minute).Format(time.RFC3339Nano), "codex_7d_used_percent": 33.0}
	applied, err = boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(ctx, a, patch)
	require.NoError(t, err)
	require.False(t, applied, "late old credential cannot advance quota with a newer time")
	a.Credentials = row["credentials"].(map[string]any)
	applied, err = boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(ctx, a, patch)
	require.NoError(t, err)
	require.True(t, applied)
	weeklyFencePGReload(t, a)
	require.Equal(t, 33.0, a.Extra["codex_7d_used_percent"])
}

func TestOpenAIWeeklyFencePGRevisionPreventsABA(t *testing.T) {
	a := weeklyFencePGAccount(t)
	stale := *a
	original := a.Extra[openAIWeeklyStateBaselineKey].(map[string]any)
	repaired := maps.Clone(original)
	repaired["baseline_percent"] = 11.0
	require.True(t, weeklyFencePGWrite(t, a, repaired))
	weeklyFencePGReload(t, a)
	require.True(t, weeklyFencePGWrite(t, a, original))
	weeklyFencePGReload(t, a)
	require.Equal(t, original, a.Extra[openAIWeeklyStateBaselineKey])
	require.Equal(t, 3.0, a.Extra[service.OpenAIWeeklyStateRevisionKey])
	require.False(t, weeklyFencePGWrite(t, &stale, repaired), "all old JSON/time guards match again; revision must reject ABA")
}

func TestOpenAIWeeklyFencePGWaitingLegacyFormKeepsCommittedRepair(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := weeklyFencePGAccount(t)
	stale, err := json.Marshal(a.Extra)
	require.NoError(t, err)
	repaired := maps.Clone(a.Extra[openAIWeeklyStateBaselineKey].(map[string]any))
	repaired["baseline_percent"] = 11.0
	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(dbent.NewTxContext(ctx, tx), a,
		map[string]any{openAIWeeklyStateBaselineKey: repaired})
	require.NoError(t, err)
	require.True(t, applied)
	done := make(chan error, 1)
	go func() {
		_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET extra = $1::jsonb, name = 'queued legacy form' WHERE id = $2`, string(stale), a.ID)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("legacy form did not wait for the quota writer: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	require.NoError(t, tx.Commit())
	require.NoError(t, <-done)
	row := weeklyStatePGRecord(t, a.ID)
	require.Equal(t, repaired, row["extra"].(map[string]any)[openAIWeeklyStateBaselineKey])
	require.Equal(t, 2.0, row["extra"].(map[string]any)[service.OpenAIWeeklyStateRevisionKey])
	require.Equal(t, "queued legacy form", row["name"])
}

func TestOpenAIWeeklyFencePGOrdersTerminalAndAllowsRealRegression(t *testing.T) {
	a := weeklyFencePGAccount(t)
	state := a.Extra[openAIWeeklyStateBaselineKey].(map[string]any)
	at, err := time.Parse(time.RFC3339Nano, state["observed_at"].(string))
	require.NoError(t, err)
	terminal := maps.Clone(state)
	terminal["snapshot_percent"], terminal["snapshot_cost"], terminal["estimate_usd"], terminal["terminal"] = 100.0, 0.0, 0.0, true
	terminal["observed_at"] = at.Add(-time.Nanosecond).Format(time.RFC3339Nano)
	require.False(t, weeklyFencePGWrite(t, a, terminal), "PG microsecond rounding must not admit an older 100%")
	terminal["observed_at"] = at.Add(time.Nanosecond).Format(time.RFC3339Nano)
	require.True(t, weeklyFencePGWrite(t, a, terminal))
	weeklyFencePGReload(t, a)
	pending := maps.Clone(terminal)
	pending["observed_at"] = at.Add(time.Second).Format(time.RFC3339Nano)
	pending["snapshot_percent"], pending["has_weekly_estimate"], pending["terminal"] = 99.0, false, false
	pending["baseline_source"] = "percent_regression"
	delete(pending, "estimate_usd")
	require.True(t, weeklyFencePGWrite(t, a, pending), "a newer real rollback is not an old writer")
	weeklyFencePGReload(t, a)
	require.False(t, weeklyFencePGWrite(t, a, terminal))
	got := a.Extra[openAIWeeklyStateBaselineKey].(map[string]any)
	require.Equal(t, "percent_regression", got["baseline_source"])
}

func TestOpenAIWeeklyFencePGTokenRotationIdentityChangeAndReset(t *testing.T) {
	a := weeklyFencePGAccount(t)
	before := maps.Clone(a.Extra)
	_, err := integrationDB.Exec(`UPDATE accounts SET credentials = credentials || '{"access_token":"synthetic-renewed"}'::jsonb,
		extra = '{"ordinary":"reauthorized","codex_7d_estimate_baseline":{"version":14}}'::jsonb WHERE id = $1`, a.ID)
	require.NoError(t, err)
	row := weeklyStatePGRecord(t, a.ID)
	require.Equal(t, before[openAIWeeklyStateBaselineKey], row["extra"].(map[string]any)[openAIWeeklyStateBaselineKey])
	require.Equal(t, "synthetic-renewed", row["credentials"].(map[string]any)["access_token"])
	_, err = integrationDB.Exec(`UPDATE accounts SET credentials = credentials || '{"chatgpt_account_id":"synthetic-new-principal"}'::jsonb WHERE id = $1`, a.ID)
	require.NoError(t, err)
	row = weeklyStatePGRecord(t, a.ID)
	a.Extra, a.Credentials = row["extra"].(map[string]any), row["credentials"].(map[string]any)
	require.NotContains(t, a.Extra, openAIWeeklyStateBaselineKey)
	require.NotContains(t, a.Extra, "codex_7d_used_percent")
	require.Equal(t, 2.0, a.Extra[service.OpenAIWeeklyStateRevisionKey])
	_, err = integrationDB.Exec(`UPDATE accounts SET extra = extra || '{"codex_7d_estimate_baseline":{"version":14}}'::jsonb WHERE id = $1`, a.ID)
	require.NoError(t, err)
	weeklyFencePGReload(t, a)
	require.NotContains(t, a.Extra, openAIWeeklyStateBaselineKey, "the cleared state still has a persistent writer fence")
	next := weeklyFencePGState("synthetic-new-principal", time.Now().UTC().Add(-time.Minute))
	require.True(t, weeklyFencePGWrite(t, a, next))
	weeklyFencePGReload(t, a)
	reset := maps.Clone(next)
	reset["observed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	reset["reset_at"] = time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)
	reset["snapshot_percent"], reset["snapshot_cost"] = 0.0, 0.0
	reset["mode"], reset["baseline_source"] = "legacy_rule_b", "weekly_window_reset"
	reset["has_weekly_estimate"], reset["awaiting_interval"] = false, true
	delete(reset, "estimate_usd")
	require.True(t, weeklyFencePGWrite(t, a, reset))
	weeklyFencePGReload(t, a)
	require.Equal(t, 4.0, a.Extra[service.OpenAIWeeklyStateRevisionKey])
	want, err := json.Marshal(reset)
	require.NoError(t, err)
	got, err := json.Marshal(a.Extra[openAIWeeklyStateBaselineKey])
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}

func TestOpenAIWeeklyFencePGPrincipalRoundTripCannotResurrectState(t *testing.T) {
	for _, field := range []string{"chatgpt_account_id", "chatgpt_user_id"} {
		t.Run(field, func(t *testing.T) {
			a := weeklyFencePGAccount(t)
			old, err := json.Marshal(a.Extra)
			require.NoError(t, err)
			original := a.Credentials[field]
			for _, value := range []any{"synthetic-other-principal", original} {
				encoded, err := json.Marshal(map[string]any{field: value})
				require.NoError(t, err)
				_, err = integrationDB.Exec(`UPDATE accounts SET credentials = credentials || $1::jsonb WHERE id = $2`, string(encoded), a.ID)
				require.NoError(t, err)
			}
			_, err = integrationDB.Exec(`UPDATE accounts SET extra = $1::jsonb WHERE id = $2`, string(old), a.ID)
			require.NoError(t, err)
			weeklyFencePGReload(t, a)
			require.Equal(t, 3.0, a.Extra[service.OpenAIWeeklyStateRevisionKey])
			require.NotContains(t, a.Extra, openAIWeeklyStateBaselineKey)
		})
	}
}

func TestOpenAIWeeklyFencePGMalformedStateRejectedWithoutFalseCAS(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(s map[string]any) { s["version"] = 14 },
		func(s map[string]any) { s["observed_at"] = "invalid" },
		func(s map[string]any) { delete(s, "observed_at") },
		func(s map[string]any) { s["identity"] = "unrelated-principal" },
	} {
		a := weeklyFencePGAccount(t)
		before := weeklyStatePGRecord(t, a.ID)
		state := maps.Clone(a.Extra[openAIWeeklyStateBaselineKey].(map[string]any))
		change(state)
		require.False(t, weeklyFencePGWrite(t, a, state))
		require.Equal(t, before, weeklyStatePGRecord(t, a.ID))
	}
	a := weeklyFencePGAccount(t)
	pending := maps.Clone(a.Extra[openAIWeeklyStateBaselineKey].(map[string]any))
	pending["observed_at"], pending["snapshot_cost"] = time.Now().UTC().Format(time.RFC3339Nano), 10.0
	pending["baseline_source"], pending["has_weekly_estimate"], pending["awaiting_interval"] = "cost_regression", false, true
	delete(pending, "estimate_usd")
	require.True(t, weeklyFencePGWrite(t, a, pending), "newer local-cost rollback must still enter pending")
}

func TestOpenAIWeeklyFencePGLegacyOnlyAndOtherPlatformsUnchanged(t *testing.T) {
	for _, scope := range [][2]string{{"openai", "oauth"}, {"openai", "apikey"}, {"anthropic", "oauth"}} {
		a := weeklyStatePGAccount(t, map[string]any{}, map[string]any{openAIWeeklyStateBaselineKey: map[string]any{"version": 14}})
		_, err := integrationDB.Exec(`UPDATE accounts SET platform = $1, type = $2 WHERE id = $3`, scope[0], scope[1], a.ID)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`UPDATE accounts SET extra = extra || '{"codex_7d_estimate_baseline":{"version":14,"snapshot_cost":40}}'::jsonb WHERE id = $1`, a.ID)
		require.NoError(t, err)
		weeklyFencePGReload(t, a)
		require.Equal(t, 40.0, a.Extra[openAIWeeklyStateBaselineKey].(map[string]any)["snapshot_cost"])
		require.NotContains(t, a.Extra, service.OpenAIWeeklyStateRevisionKey)
	}
}

func TestOpenAIWeeklyFencePGHigherVersionsAndStateClear(t *testing.T) {
	a := weeklyFencePGAccount(t)
	future := maps.Clone(a.Extra[openAIWeeklyStateBaselineKey].(map[string]any))
	future["version"] = 16.0
	require.True(t, weeklyFencePGWrite(t, a, future))
	weeklyFencePGReload(t, a)
	lower := maps.Clone(future)
	lower["version"], lower["observed_at"] = 15.0, time.Now().UTC().Format(time.RFC3339Nano)
	require.False(t, weeklyFencePGWrite(t, a, lower))
	applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(context.Background(), a,
		map[string]any{openAIWeeklyStateBaselineKey: nil, openAIWeeklyStateEpochKey: "new-epoch"})
	require.NoError(t, err)
	require.True(t, applied)
	weeklyFencePGReload(t, a)
	require.Nil(t, a.Extra[openAIWeeklyStateBaselineKey])
	require.Equal(t, 3.0, a.Extra[service.OpenAIWeeklyStateRevisionKey])
	_, err = integrationDB.Exec(`UPDATE accounts SET extra = '{}'::jsonb WHERE id = $1`, a.ID)
	require.NoError(t, err)
	weeklyFencePGReload(t, a)
	require.Equal(t, 3.0, a.Extra[service.OpenAIWeeklyStateRevisionKey])
	require.Equal(t, "new-epoch", a.Extra[openAIWeeklyStateEpochKey])
}

func TestOpenAIWeeklyFencePGMigrationBootstrapAndRepeat(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	// Model the schema immediately before 239, inside a rolled-back transaction.
	_, err = tx.ExecContext(ctx, `DROP TRIGGER accounts_guard_openai_weekly_state ON accounts`)
	require.NoError(t, err)
	state := weeklyFencePGState("synthetic-bootstrap", time.Now().UTC())
	extra, err := json.Marshal(map[string]any{openAIWeeklyStateBaselineKey: state, "ordinary": "keep"})
	require.NoError(t, err)
	var id int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,extra)
		VALUES('weekly-fence-bootstrap','openai','oauth','{"chatgpt_account_id":"synthetic-bootstrap"}'::jsonb,$1::jsonb) RETURNING id`, string(extra)).Scan(&id))
	// Include any fields produced by existing, independently owned triggers.
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT extra FROM accounts WHERE id = $1`, id).Scan(&extra))
	sql, err := migrations.FS.ReadFile("239_fence_openai_weekly_state_writers.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(sql))
		require.NoError(t, err)
		var got []byte
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT extra FROM accounts WHERE id = $1`, id).Scan(&got))
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(got, &decoded))
		require.Equal(t, 1.0, decoded[service.OpenAIWeeklyStateRevisionKey])
		delete(decoded, service.OpenAIWeeklyStateRevisionKey)
		encoded, err := json.Marshal(decoded)
		require.NoError(t, err)
		require.JSONEq(t, string(extra), string(encoded), "migration must not reconstruct any baseline/evidence")
	}
}

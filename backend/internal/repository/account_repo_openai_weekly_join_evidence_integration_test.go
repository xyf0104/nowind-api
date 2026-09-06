//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type weeklyJoinForbiddenScheduler struct{ service.SchedulerCache }

func weeklyJoinPGRepo() service.OpenAIWeeklyJoinEvidenceRepository {
	return NewAccountRepository(integrationEntClient, integrationDB, &weeklyJoinForbiddenScheduler{}).(service.OpenAIWeeklyJoinEvidenceRepository)
}

func weeklyJoinPGFixture(t *testing.T) (*service.Account, weeklyJoinWindow) {
	t.Helper()
	w := weeklyJoinTestWindow()
	w.createdAt = time.Now().UTC().Truncate(time.Microsecond).Add(-24 * time.Hour)
	w.now = w.createdAt.Add(24*time.Hour - time.Second)
	w.resetAt = w.createdAt.Add(6 * 24 * time.Hour)
	w.firstPaid = sql.NullTime{Time: w.createdAt.Add(10 * time.Minute), Valid: true}
	w.firstNonzero = w.firstPaid
	a := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name: "weekly-join-evidence-synthetic", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": w.identity, "access_token": "synthetic-not-a-real-token"},
		Extra:       map[string]any{"unrelated": "unchanged"}, CreatedAt: w.createdAt,
	})
	w.accountID = a.ID
	return a, w
}

func weeklyJoinPGAudit(t *testing.T, w weeklyJoinWindow, body string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO audit_logs
		(created_at,request_body,extra,status_code,method,path)
		VALUES ($1,$2,jsonb_build_object('params',jsonb_build_object('id',$3::text)),200,'PUT','/api/v1/admin/accounts/:id') RETURNING id`,
		w.now, body, fmt.Sprint(w.accountID)).Scan(&id))
	return id
}

func weeklyJoinPGUsage(t *testing.T, w weeklyJoinWindow, at time.Time, statsCost any, totalCost, actualCost float64, multiplier any, groupID any) int64 {
	t.Helper()
	u := mustCreateUser(t, testEntClient(t), &service.User{})
	k := mustCreateApiKey(t, testEntClient(t), &service.APIKey{UserID: u.ID, Key: fmt.Sprintf("synthetic-weekly-join-%d", u.ID), Name: "weekly-join"})
	var id int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO usage_logs
		(user_id,api_key_id,account_id,model,created_at,account_stats_cost,total_cost,actual_cost,account_rate_multiplier,group_id)
		VALUES ($1,$2,$3,'synthetic-model',$4,$5,$6,$7,$8,$9) RETURNING id`,
		u.ID, k.ID, w.accountID, at, statsCost, totalCost, actualCost, multiplier, groupID).Scan(&id))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE id=$1`, id)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM api_keys WHERE id=$1`, k.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, u.ID)
		require.NoError(t, err)
	})
	return id
}

func weeklyJoinPGAccountRecord(t *testing.T, id int64) string {
	t.Helper()
	var row string
	require.NoError(t, integrationDB.QueryRow(`SELECT to_jsonb(a)::text FROM accounts a WHERE id=$1`, id).Scan(&row))
	return row
}

func TestOpenAIWeeklyJoinEvidencePGOriginsReadOnly(t *testing.T) {
	for _, tc := range []struct {
		percent  float64
		imported bool
	}{{0, false}, {17, false}, {11, true}} {
		t.Run(fmt.Sprint(tc.percent), func(t *testing.T) {
			a, w := weeklyJoinPGFixture(t)
			body := weeklyJoinTestBody(w, tc.percent, tc.imported)
			if !tc.imported {
				body["extra"].(map[string]any)["codex_usage_updated_at"] = w.createdAt.Add(time.Hour).Format(time.RFC3339Nano)
				body["extra"].(map[string]any)["codex_7d_used_percent"] = 72
			}
			id := weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, body))
			weeklyJoinPGUsage(t, w, w.firstPaid.Time, 2, 10, 0, nil, nil)
			before := weeklyJoinPGAccountRecord(t, a.ID)
			var beforeOutbox int
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, a.ID).Scan(&beforeOutbox))
			e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
			require.NoError(t, err)
			require.NotNil(t, e)
			require.Equal(t, id, e.AuditID)
			require.Equal(t, tc.percent, e.Percent)
			require.Zero(t, e.Cost)
			require.Equal(t, a.ID, e.AccountID)
			require.Equal(t, w.identity, e.Identity)
			if tc.imported {
				require.Equal(t, service.OpenAIWeeklyJoinEvidenceImportedCorroboration, e.Kind)
				require.Equal(t, w.createdAt.Add(time.Minute), e.ObservedAt)
			} else {
				require.Equal(t, service.OpenAIWeeklyJoinEvidenceLocalInception, e.Kind)
				require.Equal(t, w.createdAt.Add(2*time.Second), e.ObservedAt)
			}
			require.Equal(t, before, weeklyJoinPGAccountRecord(t, a.ID))
			var afterOutbox int
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, a.ID).Scan(&afterOutbox))
			require.Equal(t, beforeOutbox, afterOutbox)
		})
	}
}

func TestOpenAIWeeklyJoinEvidencePGCanonicalZeroCostProof(t *testing.T) {
	group := mustCreateGroup(t, testEntClient(t), &service.Group{Name: "weekly-join-cost-ratio-" + time.Now().Format(time.RFC3339Nano), Platform: service.PlatformOpenAI})
	_, err := integrationDB.Exec(`UPDATE groups SET cost_ratio=0 WHERE id=$1`, group.ID)
	require.NoError(t, err)
	for _, tc := range []struct {
		name              string
		stats             any
		total, actual     float64
		multiplier, group any
		offset            time.Duration
		want              bool
	}{
		{"account stats paid user free", 2, 0, 0, nil, nil, time.Second, false},
		{"total fallback paid user free", nil, 2, 0, nil, nil, time.Second, false},
		{"explicit account stats zero wins", 0, 2, 999, nil, nil, time.Second, true},
		{"user charges not account cost", nil, 0, 999, nil, nil, time.Second, true},
		{"account multiplier zero", nil, 2, 999, 0, nil, time.Second, true},
		{"group ratio overrides multiplier", nil, 2, 999, 3, group.ID, time.Second, true},
		{"negative adjustment not zero", -2, 0, 0, nil, nil, time.Second, false},
		{"nonfinite account cost", "NaN", 0, 0, nil, nil, time.Second, false},
		{"equal timestamp conservative", 2, 0, 0, nil, nil, 2 * time.Second, false},
		{"paid after join", 2, 0, 0, nil, nil, 3 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, w := weeklyJoinPGFixture(t)
			weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 17, false)))
			weeklyJoinPGUsage(t, w, w.createdAt.Add(tc.offset), tc.stats, tc.total, tc.actual, tc.multiplier, tc.group)
			e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
			require.NoError(t, err)
			require.Equal(t, tc.want, e != nil)
		})
	}
	t.Run("positive negative cancellation", func(t *testing.T) {
		a, w := weeklyJoinPGFixture(t)
		weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 0, false)))
		weeklyJoinPGUsage(t, w, w.createdAt.Add(time.Second), 5, 0, 0, nil, nil)
		weeklyJoinPGUsage(t, w, w.createdAt.Add(time.Second), -5, 0, 0, nil, nil)
		e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
		require.NoError(t, err)
		require.Nil(t, e)
	})
}

func TestOpenAIWeeklyJoinEvidencePGScopeAndUncertainBodies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, weeklyJoinWindow, int64)
	}{
		{"other account", func(t *testing.T, w weeklyJoinWindow, id int64) {
			_, err := integrationDB.Exec(`UPDATE audit_logs SET extra='{"params":{"id":"unrelated"}}' WHERE id=$1`, id)
			require.NoError(t, err)
		}},
		{"missing account param", func(t *testing.T, w weeklyJoinWindow, id int64) {
			_, err := integrationDB.Exec(`UPDATE audit_logs SET extra='{}' WHERE id=$1`, id)
			require.NoError(t, err)
		}},
		{"failed request", func(t *testing.T, w weeklyJoinWindow, id int64) {
			_, err := integrationDB.Exec(`UPDATE audit_logs SET status_code=400 WHERE id=$1`, id)
			require.NoError(t, err)
		}},
		{"redirect", func(t *testing.T, w weeklyJoinWindow, id int64) {
			_, err := integrationDB.Exec(`UPDATE audit_logs SET status_code=302 WHERE id=$1`, id)
			require.NoError(t, err)
		}},
		{"unrelated route", func(t *testing.T, w weeklyJoinWindow, id int64) {
			_, err := integrationDB.Exec(`UPDATE audit_logs SET path='/api/v1/admin/users/:id' WHERE id=$1`, id)
			require.NoError(t, err)
		}},
		{"generic import", func(t *testing.T, w weeklyJoinWindow, id int64) {
			_, err := integrationDB.Exec(`UPDATE audit_logs SET path='/api/v1/admin/accounts/data',method='POST' WHERE id=$1`, id)
			require.NoError(t, err)
		}},
		{"before local creation", func(t *testing.T, w weeklyJoinWindow, id int64) {
			_, err := integrationDB.Exec(`UPDATE audit_logs SET created_at=$2 WHERE id=$1`, id, w.createdAt.Add(-time.Second))
			require.NoError(t, err)
		}},
		{"future audit", func(t *testing.T, w weeklyJoinWindow, id int64) {
			_, err := integrationDB.Exec(`UPDATE audit_logs SET created_at=CURRENT_TIMESTAMP+interval '1 hour' WHERE id=$1`, id)
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, w := weeklyJoinPGFixture(t)
			id := weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 0, false)))
			tc.mutate(t, w, id)
			e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
			require.NoError(t, err)
			require.Nil(t, e)
		})
	}
	for _, tc := range []struct {
		name     string
		mutate   func(map[string]any)
		imported bool
	}{
		{"missing cost", func(b map[string]any) { delete(weeklyJoinTestBaseline(b), "snapshot_cost") }, false},
		{"null cost", func(b map[string]any) { weeklyJoinTestBaseline(b)["snapshot_cost"] = nil }, false},
		{"positive later baseline", func(b map[string]any) {
			weeklyJoinTestBaseline(b)["snapshot_cost"] = 120
			weeklyJoinTestBaseline(b)["observed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		}, false},
		{"other identity", func(b map[string]any) { weeklyJoinTestBaseline(b)["identity"] = "other" }, false},
		{"wrong reset", func(b map[string]any) { weeklyJoinTestBaseline(b)["reset_at"] = "2001-01-01T00:00:00Z" }, false},
		{"one hundred", func(b map[string]any) { weeklyJoinTestBaseline(b)["snapshot_percent"] = 100 }, false},
		{"raw only no nested cost", func(b map[string]any) { delete(b["extra"].(map[string]any), "codex_7d_estimate_baseline") }, true},
		{"import old timestamp only", func(b map[string]any) { delete(b["extra"].(map[string]any), "codex_usage_updated_at") }, true},
		{"import changed raw percentage", func(b map[string]any) { b["extra"].(map[string]any)["codex_7d_used_percent"] = 12 }, true},
		{"import no raw identity", func(b map[string]any) { delete(b, "credentials") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, w := weeklyJoinPGFixture(t)
			body := weeklyJoinTestBody(w, 11, tc.imported)
			tc.mutate(body)
			weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, body))
			e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
			require.NoError(t, err)
			require.Nil(t, e)
		})
	}
	for _, body := range []string{`{"extra":`, "<redacted>", `{"extra":null}`, `{"extra":{},"extra":{}}`, strings.Repeat("x", weeklyJoinBodyLimit+1)} {
		a, w := weeklyJoinPGFixture(t)
		weeklyJoinPGAudit(t, w, body)
		e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
		require.NoError(t, err)
		require.Nil(t, e)
	}
}

func TestOpenAIWeeklyJoinEvidencePGLateZeroAndImportPaidReject(t *testing.T) {
	for _, imported := range []bool{false, true} {
		a, w := weeklyJoinPGFixture(t)
		body := weeklyJoinTestBody(w, 11, imported)
		if !imported {
			weeklyJoinTestBaseline(body)["observed_at"] = w.createdAt.Add(time.Minute).Format(time.RFC3339Nano)
		}
		weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, body))
		weeklyJoinPGUsage(t, w, w.createdAt.Add(30*time.Second), 1, 0, 0, nil, nil)
		e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
		require.NoError(t, err)
		require.Nil(t, e, "an explicit old zero does not override independent paid-use evidence")
	}
}

func TestOpenAIWeeklyJoinEvidencePGAmbiguityAndBoundedHistory(t *testing.T) {
	a, w := weeklyJoinPGFixture(t)
	body := weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 17, false))
	first := weeklyJoinPGAudit(t, w, body)
	weeklyJoinPGAudit(t, w, body)
	e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
	require.NoError(t, err)
	require.NotNil(t, e)
	require.Equal(t, first, e.AuditID)
	weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 11, false)))
	e, err = weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
	require.NoError(t, err)
	require.Nil(t, e, "conflicting zero-cost origins are not guessed")

	a, w = weeklyJoinPGFixture(t)
	body = weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 0, false))
	_, err = integrationDB.Exec(`INSERT INTO audit_logs(created_at,request_body,extra,status_code,method,path)
		SELECT $1,$2,jsonb_build_object('params',jsonb_build_object('id',$3::text)),200,'PUT','/api/v1/admin/accounts/:id' FROM generate_series(1,$4::int)`,
		w.now, body, fmt.Sprint(a.ID), weeklyJoinAuditLimit)
	require.NoError(t, err)
	e, err = weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
	require.NoError(t, err)
	require.NotNil(t, e)
	weeklyJoinPGAudit(t, w, body)
	e, err = weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
	require.NoError(t, err)
	require.Nil(t, e, "a partial bounded scan cannot establish unique inception")
}

func TestOpenAIWeeklyJoinEvidencePGCurrentAccountGuards(t *testing.T) {
	for _, change := range []string{
		`credentials=jsonb_build_object('chatgpt_account_id','different')`,
		`credentials='{"chatgpt_account_id":null}'`,
		`platform='anthropic'`, `type='apikey'`, `deleted_at=NOW()`,
		`created_at=created_at-interval '8 days'`, `created_at=NOW()+interval '1 hour'`,
	} {
		a, w := weeklyJoinPGFixture(t)
		weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 0, false)))
		_, err := integrationDB.Exec(`UPDATE accounts SET `+change+` WHERE id=$1`, a.ID)
		require.NoError(t, err)
		e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
		require.NoError(t, err)
		require.Nil(t, e)
	}
	a, w := weeklyJoinPGFixture(t)
	weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 0, false)))
	for _, reset := range []time.Time{w.createdAt, time.Now().Add(8 * 24 * time.Hour), w.resetAt.Add(2 * time.Minute)} {
		e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, reset)
		require.NoError(t, err)
		require.Nil(t, e)
	}
	a.ID = 1 << 60
	e, err := weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
	require.NoError(t, err)
	require.Nil(t, e)
}

func TestOpenAIWeeklyJoinEvidencePGEntSnapshotAndRollback(t *testing.T) {
	ctx := context.Background()
	a, w := weeklyJoinPGFixture(t)
	weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 17, false)))
	before := weeklyJoinPGAccountRecord(t, a.ID)
	tx, err := integrationEntClient.BeginTx(ctx, &entsql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	txCtx := dbent.NewTxContext(ctx, tx)
	r := newAccountRepositoryWithSQL(nil, nil, &weeklyJoinForbiddenScheduler{})
	e, err := r.FindOpenAIWeeklyJoinEvidence(txCtx, a, w.resetAt)
	require.NoError(t, err)
	require.NotNil(t, e)
	// These committed concurrent changes must not leak into the existing snapshot.
	weeklyJoinPGUsage(t, w, w.createdAt.Add(time.Second), 5, 0, 0, nil, nil)
	weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 11, false)))
	e2, err := r.FindOpenAIWeeklyJoinEvidence(txCtx, a, w.resetAt)
	require.NoError(t, err)
	require.Equal(t, e, e2)
	require.NoError(t, tx.Rollback(), "resolver does not commit or roll back the caller's transaction")
	require.Equal(t, before, weeklyJoinPGAccountRecord(t, a.ID))
	e, err = weeklyJoinPGRepo().FindOpenAIWeeklyJoinEvidence(ctx, a, w.resetAt)
	require.NoError(t, err)
	require.Nil(t, e, "a fresh snapshot sees newly committed billing")

	for _, opts := range []*entsql.TxOptions{
		{Isolation: sql.LevelReadCommitted, ReadOnly: true}, {Isolation: sql.LevelRepeatableRead, ReadOnly: false},
		{Isolation: sql.LevelSerializable, ReadOnly: true},
	} {
		tx, err := integrationEntClient.BeginTx(ctx, opts)
		require.NoError(t, err)
		_, err = r.FindOpenAIWeeklyJoinEvidence(dbent.NewTxContext(ctx, tx), a, w.resetAt)
		if opts.Isolation == sql.LevelSerializable {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, service.ErrOpenAIWeeklyJoinEvidenceSnapshotRequired)
		}
		require.NoError(t, tx.Rollback())
	}
}

type weeklyJoinPGInspectDB struct {
	*sql.DB
	opts                sql.TxOptions
	isolation, readOnly string
}

func (d *weeklyJoinPGInspectDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	d.opts = *opts
	tx, err := d.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT current_setting('transaction_isolation'),current_setting('transaction_read_only')`).Scan(&d.isolation, &d.readOnly); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func TestOpenAIWeeklyJoinEvidencePGOwnSnapshotIsReadOnly(t *testing.T) {
	a, w := weeklyJoinPGFixture(t)
	weeklyJoinPGAudit(t, w, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 0, false)))
	checked := &weeklyJoinPGInspectDB{DB: integrationDB}
	r := newAccountRepositoryWithSQL(nil, checked, &weeklyJoinForbiddenScheduler{})
	e, err := r.FindOpenAIWeeklyJoinEvidence(context.Background(), a, w.resetAt)
	require.NoError(t, err)
	require.NotNil(t, e)
	require.True(t, checked.opts.ReadOnly)
	require.Equal(t, sql.LevelRepeatableRead, checked.opts.Isolation)
	require.Equal(t, "repeatable read", checked.isolation)
	require.Equal(t, "on", checked.readOnly)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e, err = r.FindOpenAIWeeklyJoinEvidence(ctx, a, w.resetAt)
	require.Nil(t, e)
	require.ErrorIs(t, err, context.Canceled)
	// The input object is never hydrated, rewritten or populated with evidence.
	encoded, err := json.Marshal(a.Extra)
	require.NoError(t, err)
	require.JSONEq(t, `{"unrelated":"unchanged"}`, string(encoded))
}

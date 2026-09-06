//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"maps"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWeeklyFencePGRawFullCredentialABA(t *testing.T) {
	for _, combined := range []bool{false, true} {
		name := "gateway"
		if combined {
			name = "quota"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			a := weeklyFencePGAccount(t)
			credentials, err := json.Marshal(a.Credentials)
			require.NoError(t, err)
			_, err = integrationDB.Exec(`UPDATE accounts SET credentials = credentials || '{"chatgpt_user_id":"synthetic-other-user"}'::jsonb WHERE id = $1`, a.ID)
			require.NoError(t, err)
			_, err = integrationDB.Exec(`UPDATE accounts SET credentials = $1::jsonb WHERE id = $2`, string(credentials), a.ID)
			require.NoError(t, err)
			before := weeklyStatePGRecord(t, a.ID)
			require.Equal(t, a.Credentials, before["credentials"], "complete credentials, including tokens, have returned to A")
			require.Equal(t, 3.0, before["extra"].(map[string]any)[service.OpenAIWeeklyStateRevisionKey])
			at := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
			patch := map[string]any{"codex_usage_updated_at": at.Format(time.RFC3339Nano), "codex_7d_used_percent": 99.0}
			write := func(expected *service.Account) (bool, error) {
				if combined {
					return boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, expected, expected, at, patch)
				}
				return boundCodexPGRepo().UpdateOpenAICodexSnapshotIfIdentityMatches(ctx, expected, patch)
			}
			for _, extra := range []map[string]any{a.Extra, nil, {service.OpenAIWeeklyStateRevisionKey: nil}} {
				stale := *a
				stale.Extra = extra
				applied, err := write(&stale)
				require.NoError(t, err)
				require.False(t, applied, "old, absent and null fences must not match the current revision")
				require.Equal(t, before, weeklyStatePGRecord(t, a.ID))
			}
			weeklyFencePGReload(t, a)
			applied, err := write(a)
			require.NoError(t, err)
			require.True(t, applied, "the authoritative post-change RAW capture must remain usable")
			weeklyFencePGReload(t, a)
			require.Equal(t, 99.0, a.Extra["codex_7d_used_percent"])
			require.Equal(t, 3.0, a.Extra[service.OpenAIWeeklyStateRevisionKey], "RAW cannot advance the revision")
			require.NotContains(t, a.Extra, openAIWeeklyStateBaselineKey, "RAW must not invent an inception")
		})
	}
}

func TestOpenAIWeeklyFencePGSparkRawTargetAndParentABA(t *testing.T) {
	for _, changedRow := range []string{"target", "parent"} {
		t.Run(changedRow, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			parent, target := weeklyFencePGAccount(t), weeklyFencePGAccount(t)
			_, err := integrationDB.Exec(`UPDATE accounts SET parent_account_id = $2, quota_dimension = 'spark', credentials = '{}'::jsonb WHERE id = $1`, target.ID, parent.ID)
			require.NoError(t, err)
			target.ParentAccountID, target.QuotaDimension, target.Credentials = &parent.ID, service.QuotaDimensionSpark, map[string]any{}
			weeklyFencePGReload(t, target)
			changed := parent
			if changedRow == "target" {
				changed = target
			}
			original, err := json.Marshal(changed.Credentials)
			require.NoError(t, err)
			tx, err := integrationEntClient.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = tx.Client().ExecContext(ctx, `UPDATE accounts SET credentials = '{"chatgpt_account_id":"synthetic-intermediate"}'::jsonb WHERE id = $1`, changed.ID)
			require.NoError(t, err)
			_, err = tx.Client().ExecContext(ctx, `UPDATE accounts SET credentials = $1::jsonb WHERE id = $2`, string(original), changed.ID)
			require.NoError(t, err)
			conn, err := integrationDB.Conn(ctx)
			require.NoError(t, err)
			defer conn.Close()
			var pid int
			require.NoError(t, conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
			at := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
			type outcome struct {
				applied bool
				err     error
			}
			done := make(chan outcome, 1)
			go func() {
				applied, err := (&accountRepository{sql: conn}).UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, target, parent, at, boundQuotaPGPatch(at))
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
			require.False(t, result.applied, "revision must be rechecked after the row-lock wait, even when credentials are identical")
			row := weeklyStatePGRecord(t, changed.ID)
			require.Equal(t, changed.Credentials, row["credentials"])
			require.Equal(t, changed.Extra[service.OpenAIWeeklyStateRevisionKey].(float64)+2, row["extra"].(map[string]any)[service.OpenAIWeeklyStateRevisionKey])
			require.NotContains(t, weeklyStatePGRecord(t, target.ID)["extra"].(map[string]any), "codex_7d_used_percent")
			weeklyFencePGReload(t, parent)
			weeklyFencePGReload(t, target)
			applied, err := boundCodexPGRepo().UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, target, parent, at, boundQuotaPGPatch(at))
			require.NoError(t, err)
			require.True(t, applied)
		})
	}
}

func TestOpenAIWeeklyFencePGUserIdentityCompletenessBoundary(t *testing.T) {
	for _, writer := range []string{"old full form", "current repository"} {
		for _, tc := range []struct {
			name      string
			before    map[string]any
			after     map[string]any
			preserved bool
		}{
			{"known unchanged", map[string]any{"chatgpt_user_id": "user-a"}, map[string]any{"chatgpt_user_id": "user-a"}, true},
			{"absent unchanged", nil, nil, true},
			{"empty unchanged", map[string]any{"chatgpt_user_id": ""}, map[string]any{"chatgpt_user_id": ""}, true},
			{"null to absent", map[string]any{"chatgpt_user_id": nil}, nil, true},
			{"absent to known", nil, map[string]any{"chatgpt_user_id": "user-a"}, false},
			{"empty to known", map[string]any{"chatgpt_user_id": ""}, map[string]any{"chatgpt_user_id": "user-a"}, false},
			{"null to known", map[string]any{"chatgpt_user_id": nil}, map[string]any{"chatgpt_user_id": "user-a"}, false},
			{"known to absent", map[string]any{"chatgpt_user_id": "user-a"}, nil, false},
			{"known different", map[string]any{"chatgpt_user_id": "user-a"}, map[string]any{"chatgpt_user_id": "user-b"}, false},
		} {
			t.Run(writer+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				credentials := map[string]any{"chatgpt_account_id": "synthetic-identity-completeness", "access_token": "synthetic-old"}
				maps.Copy(credentials, tc.before)
				at := time.Now().UTC().Add(-time.Minute)
				a := weeklyStatePGAccount(t, credentials, map[string]any{
					openAIWeeklyStateBaselineKey: weeklyFencePGState(credentials["chatgpt_account_id"].(string), at),
					openAIWeeklyStateEpochKey:    "synthetic-epoch", "codex_7d_used_percent": 21.0,
					openAIWeeklyStateUsageUpdatedKey: at.Format(time.RFC3339Nano), "ordinary": "keep"})
				repo := NewAccountRepository(integrationEntClient, integrationDB, nil)
				form, err := repo.GetByID(ctx, a.ID)
				require.NoError(t, err)
				accepted := maps.Clone(form.Extra)
				delete(form.Credentials, "chatgpt_user_id")
				maps.Copy(form.Credentials, tc.after)
				form.Credentials["access_token"] = "synthetic-rotated"
				form.Name, form.Extra["ordinary"] = "identity edit accepted", "edited"
				if writer == "current repository" {
					require.NoError(t, repo.Update(ctx, form))
				} else {
					encoded, err := json.Marshal(form.Credentials)
					require.NoError(t, err)
					extra, err := json.Marshal(form.Extra)
					require.NoError(t, err)
					_, err = integrationDB.Exec(`UPDATE accounts SET credentials = $1::jsonb, extra = $2::jsonb, name = $3 WHERE id = $4`, string(encoded), string(extra), form.Name, a.ID)
					require.NoError(t, err)
				}
				got, err := repo.GetByID(ctx, a.ID)
				require.NoError(t, err)
				require.Equal(t, form.Name, got.Name)
				require.Equal(t, "edited", got.Extra["ordinary"])
				require.Equal(t, "synthetic-rotated", got.GetCredential("access_token"))
				if tc.preserved {
					for key, value := range accepted {
						if service.IsOpenAIQuotaRuntimeExtraKey(key) {
							require.Equal(t, value, got.Extra[key], key)
						}
					}
				} else {
					require.Equal(t, 2.0, got.Extra[service.OpenAIWeeklyStateRevisionKey])
					for key := range got.Extra {
						if key != service.OpenAIWeeklyStateRevisionKey {
							require.False(t, service.IsOpenAIQuotaRuntimeExtraKey(key), "unconfirmed user must not retain %s", key)
						}
					}
				}
			})
		}
	}
}

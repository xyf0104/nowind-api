//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWeeklyJoinEntryRevisionOnlyRereadRetainsAuthoritativeFence(t *testing.T) {
	for _, lateRevision := range []bool{false, true} {
		name := "authoritative reread"
		if lateRevision {
			name = "revision advances during request"
		}
		t.Run(name, func(t *testing.T) {
			account := weeklyJoinEntryExisting()
			account.Extra[OpenAIWeeklyStateRevisionKey] = 7.0
			saved := cloneWeeklyJoinEntryAccount(account)
			saved.Extra = map[string]any{OpenAIWeeklyStateRevisionKey: 8.0}
			repo := &weeklyJoinEntryRepo{rows: map[int64]*Account{account.ID: saved}}
			server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if lateRevision {
					repo.mu.Lock()
					repo.rows[account.ID].Extra[OpenAIWeeklyStateRevisionKey] = 9.0
					repo.mu.Unlock()
				}
				writeWeeklyJoinEntryQuota(w, "old-account", "old-user", 20.4)
			})
			svc := &adminServiceImpl{accountRepo: repo, privacyClientFactory: newQuotaRedirectingFactory(server)}
			svc.captureOpenAIWeeklyJoinAfterPrincipalReplace(context.Background(), account)
			require.Equal(t, 1, repo.casCalls, "a changed revision alone must not be called a changed principal")
			require.Equal(t, json.Number("8"), repo.lastCASRevision, "capture must bind to the pre-request authoritative revision")
			require.NotContains(t, account.Extra, openAIWeeklyEstimateBaselineKey)
			if lateRevision {
				require.Zero(t, repo.casApplied)
				require.NotContains(t, account.Extra, "codex_7d_used_percent")
			} else {
				require.Equal(t, 1, repo.casApplied)
				require.Equal(t, 20.4, account.Extra["codex_7d_used_percent"])
				require.Equal(t, 8.0, account.Extra[OpenAIWeeklyStateRevisionKey])
			}
		})
	}
}

func TestWeeklyJoinPrincipalUserCompletenessIsNotCaptureEligibility(t *testing.T) {
	for _, tc := range []struct {
		before, after string
		capture       bool
	}{
		{"", "user-a", false}, {"user-a", "", false},
		{"user-a", "user-a", false}, {"user-a", "user-b", true},
	} {
		t.Run(tc.before+"/"+tc.after, func(t *testing.T) {
			a := weeklyJoinEntryExisting()
			a.Credentials["chatgpt_user_id"] = tc.before
			before := openAIWeeklyJoinPrincipalOf(a)
			a.Credentials["chatgpt_user_id"] = tc.after
			require.Equal(t, tc.capture, before.replacedBy(a), "DB historical-state retention has its own stricter identity guard")
		})
	}
}

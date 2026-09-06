package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type refreshTransitionEmptyScanHook struct {
	pages    int
	aclLines []string
}

func (h *refreshTransitionEmptyScanHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *refreshTransitionEmptyScanHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *refreshTransitionEmptyScanHook) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, command redis.Cmder) error {
		if list, ok := command.(*redis.StringSliceCmd); ok && command.Name() == "acl" {
			list.SetVal(h.aclLines)
			return nil
		}
		if scan, ok := command.(*redis.ScanCmd); ok {
			h.pages++
			scan.SetVal([]string{}, 1)
			return nil
		}
		return errors.New("unexpected command in empty-scan budget test")
	}
}

func TestPersistentRefreshTransitionExclusivePrincipalHasOnlyRecoveryPassword(t *testing.T) {
	passwordHash := strings.Repeat("a", 64)
	exclusive := "user exclusive on sanitize-payload #" + passwordHash + " ~* &* +@all"
	disabled := "user default off sanitize-payload resetchannels -@all"
	for _, fixture := range []struct {
		name, principal, old string
		valid                bool
	}{
		{"valid", exclusive, disabled, true},
		{"additional-password", exclusive + " #" + strings.Repeat("b", 64), disabled, false},
		{"nopass", exclusive + " nopass", disabled, false},
		{"off", strings.Replace(exclusive, " on ", " off ", 1), disabled, false},
		{"selector", exclusive + " (~* +@all)", disabled, false},
		{"wrong-password", strings.ReplaceAll(exclusive, passwordHash, strings.Repeat("b", 64)), disabled, false},
		{"old-enabled", exclusive, strings.Replace(disabled, " off ", " on ", 1), false},
		{"old-password-retained", exclusive, disabled + " #" + strings.Repeat("b", 64), false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
			defer func() { _ = client.Close() }()
			client.AddHook(&refreshTransitionEmptyScanHook{aclLines: []string{fixture.principal, fixture.old}})
			hash, err := refreshTransitionVerifyACL(context.Background(), client, "exclusive", passwordHash, "")
			if fixture.valid {
				require.NoError(t, err)
				require.Len(t, hash, 64)
			} else {
				require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
				require.Empty(t, hash)
			}
		})
	}
}

func TestPersistentRefreshTransitionEmptyMatchPageBudget(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	defer func() { _ = client.Close() }()
	hook := &refreshTransitionEmptyScanHook{}
	client.AddHook(hook)
	entries, err := refreshTransitionSnapshot(context.Background(), client)
	require.Nil(t, entries)
	require.ErrorContains(t, err, "scan page budget exhausted")
	require.Equal(t, refreshTransitionMaxScanPages, hook.pages, "MATCH returning no auth keys still consumes the hard page budget")
}

func refreshTransitionTestData() *service.RefreshTokenData {
	created := time.Now().UTC().Truncate(time.Microsecond).Add(-24 * time.Hour)
	return &service.RefreshTokenData{UserID: 71, TokenVersion: -53, FamilyID: strings.Repeat("a", 32), BindingHash: strings.Repeat("b", 64),
		CreatedAt: created, ExpiresAt: created.Add(7 * 24 * time.Hour), FamilyExpiresAt: created.Add(7 * 24 * time.Hour)}
}

func TestPersistentRefreshTransitionDecodePreservesOriginalDeadlines(t *testing.T) {
	d := refreshTransitionTestData()
	encoded, err := json.Marshal(d)
	require.NoError(t, err)
	got, err := refreshTransitionDecode(string(encoded))
	require.NoError(t, err)
	require.Equal(t, d, got)
	var object map[string]any
	require.NoError(t, json.Unmarshal(encoded, &object))
	delete(object, "family_expires_at")
	object["expires_at"] = d.CreatedAt.Add(3 * 24 * time.Hour).Format(time.RFC3339Nano)
	encoded, err = json.Marshal(object)
	require.NoError(t, err)
	got, err = refreshTransitionDecode(string(encoded))
	require.NoError(t, err)
	require.Equal(t, d.CreatedAt.Add(3*24*time.Hour), got.FamilyExpiresAt)
	require.Equal(t, got.ExpiresAt, got.FamilyExpiresAt)
	d.FamilyExpiresAt = d.CreatedAt.Add(8 * 24 * time.Hour)
	encoded, err = json.Marshal(d)
	require.NoError(t, err)
	_, err = refreshTransitionDecode(string(encoded))
	require.ErrorContains(t, err, "family deadline")
	secret, err := NewLegacyRefreshTransitionRecoverySecret()
	require.NoError(t, err)
	require.Len(t, secret, 32)
}

func TestPersistentRefreshTransitionAcceptsActualHTTPBinding(t *testing.T) {
	d := refreshTransitionTestData()
	d.BindingHash = (&service.SessionBinding{IP: "192.0.2.1", UserAgent: "migration-regression"}).Hash()
	require.Len(t, d.BindingHash, 32, "the established HTTP producer uses a 128-bit binding")
	body, err := json.Marshal(d)
	require.NoError(t, err)
	got, err := refreshTransitionDecode(string(body))
	require.NoError(t, err)
	require.Equal(t, d, got, "adoption must preserve, not regenerate, the existing binding")
}

func TestPersistentRefreshTransitionRejectsUntrustedMetadata(t *testing.T) {
	encoded, err := json.Marshal(refreshTransitionTestData())
	require.NoError(t, err)
	for _, bad := range []string{
		"", "null", "[]", "not-json", string(encoded) + "{}", string(encoded[:len(encoded)-1]),
		strings.Replace(string(encoded), `"user_id":71`, `"user_id":71,"user_id":72`, 1),
		strings.Replace(string(encoded), `"user_id":71`, `"user_id":null`, 1),
		strings.Replace(string(encoded), `"user_id":71`, `"user_id":"71"`, 1),
		strings.Replace(string(encoded), `"user_id":71`, `"user_id":0`, 1),
		strings.Replace(string(encoded), `"token_version":-53,`, "", 1),
		strings.Replace(string(encoded), `"token_version":-53`, `"token_version":-53,"refresh_token":"must-never-appear"`, 1),
		strings.Repeat(" ", 4097),
	} {
		d, err := refreshTransitionDecode(bad)
		require.Nil(t, d)
		require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
		require.NotContains(t, err.Error(), "must-never-appear")
	}
	var store *PersistentRefreshTokenStore
	result, err := store.AdoptLegacyRefreshTokens(context.Background(), LegacyRefreshTransitionOptions{})
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
}

func TestPersistentRefreshTransitionSevenDayMicrosecondPrecision(t *testing.T) {
	for _, nanos := range []int{0, 1, 499, 500, 999} {
		t.Run(strconv.Itoa(nanos), func(t *testing.T) {
			d := refreshTransitionTestData()
			d.CreatedAt = time.Date(2026, time.January, 2, 3, 4, 5, 123456000+nanos, time.UTC)
			d.ExpiresAt = d.CreatedAt.Add(7 * 24 * time.Hour)
			d.FamilyExpiresAt = d.ExpiresAt
			for _, legacyFamily := range []bool{false, true} {
				payload := *d
				if legacyFamily {
					payload.FamilyExpiresAt = time.Time{}
				}
				encoded, err := json.Marshal(payload)
				require.NoError(t, err)
				got, err := refreshTransitionDecode(string(encoded))
				require.NoError(t, err, "exactly seven days is valid before and after precision normalization")
				require.Equal(t, d.CreatedAt.Truncate(time.Microsecond), got.CreatedAt)
				require.Equal(t, d.ExpiresAt.Truncate(time.Microsecond), got.ExpiresAt)
				require.Equal(t, got.ExpiresAt, got.FamilyExpiresAt)
				require.Equal(t, 7*24*time.Hour, got.FamilyExpiresAt.Sub(got.CreatedAt))
				require.False(t, got.ExpiresAt.After(d.ExpiresAt), "never round expiry up")
				require.False(t, got.FamilyExpiresAt.After(d.FamilyExpiresAt), "never restart a legacy family deadline")
			}
			d.FamilyExpiresAt = d.CreatedAt.Add(7*24*time.Hour + time.Microsecond)
			encoded, err := json.Marshal(d)
			require.NoError(t, err)
			_, err = refreshTransitionDecode(string(encoded))
			require.ErrorIs(t, err, ErrRefreshTransitionUnsafe, "one microsecond beyond seven days is not a tolerated clock skew")
		})
	}
}

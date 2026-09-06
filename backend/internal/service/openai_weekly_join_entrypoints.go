package service

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
)

type openAIWeeklyJoinPrincipal struct {
	platform string
	kind     string
	account  string
	user     string
}

func openAIWeeklyJoinPrincipalOf(account *Account) openAIWeeklyJoinPrincipal {
	if account == nil {
		return openAIWeeklyJoinPrincipal{}
	}
	return openAIWeeklyJoinPrincipal{
		platform: account.Platform, kind: account.Type,
		account: strings.TrimSpace(account.GetCredential("chatgpt_account_id")),
		user:    strings.TrimSpace(account.GetCredential("chatgpt_user_id")),
	}
}

func (before openAIWeeklyJoinPrincipal) replacedBy(account *Account) bool {
	if account == nil || !account.IsOpenAIOAuth() || account.IsShadow() {
		return false
	}
	after := openAIWeeklyJoinPrincipalOf(account)
	if after.account == "" {
		return false
	}
	// This is capture eligibility, not permission to retain historical state.
	// Missing/present user changes still invalidate history in the DB guard.
	return before.platform != after.platform || before.kind != after.kind || before.account != after.account ||
		(before.user != "" && after.user != "" && before.user != after.user)
}

// Compare cloned request identities only. The authoritative re-read may have a
// newer revision after the principal-change trigger; retain it for actual CAS.
func sameOpenAIWeeklyJoinRequestIdentity(a, b *Account) bool {
	if a == nil || b == nil {
		return a == b
	}
	left, right := *a, *b
	left.Extra, right.Extra = nil, nil
	return reflect.DeepEqual(left, right)
}

func (s *adminServiceImpl) captureOpenAIWeeklyJoinAfterPrincipalReplace(ctx context.Context, account *Account) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	writer, ok := s.accountRepo.(OpenAICodexBoundSnapshotRepository)
	if !ok {
		return
	}
	expected, err := cloneOpenAICodexSnapshotIdentity(account)
	if err != nil {
		return
	}
	// The principal-change trigger may strip quota fields and advance its
	// revision without updating the object passed to Update. Read the saved row.
	saved, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil || saved == nil {
		return
	}
	savedIdentity, err := cloneOpenAICodexSnapshotIdentity(saved)
	if err != nil || !sameOpenAIWeeklyJoinRequestIdentity(expected, savedIdentity) {
		return
	}
	account.Extra = saved.Extra
	observation := s.readOpenAIWeeklyJoinObservation(ctx, saved)
	if observation == nil {
		return
	}
	updates := observation.rawUpdates()
	applied, err := writer.UpdateOpenAICodexSnapshotIfIdentityMatches(ctx, observation.account, updates)
	if err != nil {
		slog.Warn("openai_weekly_principal_join_capture_failed", "account_id", account.ID, "error", err)
		return
	}
	if applied {
		confirmed, readErr := s.accountRepo.GetByID(ctx, account.ID)
		if readErr == nil && confirmed != nil {
			confirmedIdentity, cloneErr := cloneOpenAICodexSnapshotIdentity(confirmed)
			if cloneErr == nil && sameOpenAIWeeklyJoinRequestIdentity(expected, confirmedIdentity) {
				account.Extra = confirmed.Extra
			}
		}
	}
}

func (s *CRSSyncService) openAIWeeklyJoinCaptureService() *adminServiceImpl {
	factory := s.weeklyJoinCaptureFactory
	if factory == nil && s.openaiOAuthService != nil {
		factory = s.openaiOAuthService.privacyClientFactory
	}
	return &adminServiceImpl{accountRepo: s.accountRepo, proxyRepo: s.proxyRepo, privacyClientFactory: factory}
}

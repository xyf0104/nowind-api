package service

import (
	"context"
	"errors"
	"fmt"
)

// AntigravityOAuthRefreshApplier is the narrow admin-service boundary for a
// manual Antigravity token refresh. The complete credential document and proxy
// used by the upstream attempt are compared atomically before the result is
// persisted, so an interactive re-authorization always wins the race.
type AntigravityOAuthRefreshApplier interface {
	ApplyAntigravityOAuthRefreshIfUnchanged(
		ctx context.Context,
		attemptedAccount *Account,
		credentials map[string]any,
	) (account *Account, applied bool, err error)
}

func (s *adminServiceImpl) ApplyAntigravityOAuthRefreshIfUnchanged(
	ctx context.Context,
	attemptedAccount *Account,
	credentials map[string]any,
) (*Account, bool, error) {
	if attemptedAccount == nil {
		return nil, false, errors.New("antigravity OAuth refresh account is nil")
	}
	if attemptedAccount.Platform != PlatformAntigravity || attemptedAccount.Type != AccountTypeOAuth {
		return nil, false, errors.New("account is not an Antigravity OAuth account")
	}
	if len(credentials) == 0 {
		return nil, false, errors.New("antigravity OAuth refresh credentials are empty")
	}

	conditionalRepo, ok := s.accountRepo.(AntigravityOAuthRefreshSuccessRepository)
	if !ok {
		return nil, false, errors.New("antigravity OAuth refresh success CAS repository is not configured")
	}

	credentials = shallowCopyMap(credentials)
	credentials["_token_version"] = nextOAuthTokenVersion(attemptedAccount.Credentials)
	applied, err := conditionalRepo.UpdateAntigravityOAuthCredentialsIfUnchanged(
		ctx,
		attemptedAccount.ID,
		shallowCopyMap(attemptedAccount.Credentials),
		cloneInt64Pointer(attemptedAccount.ProxyID),
		antigravityOAuthProxyUpdatedAt(attemptedAccount),
		credentials,
	)
	if err != nil {
		return nil, false, fmt.Errorf("persist Antigravity OAuth refresh with CAS: %w", err)
	}

	currentAccount, err := s.accountRepo.GetByID(ctx, attemptedAccount.ID)
	if err != nil {
		return nil, false, fmt.Errorf("read Antigravity OAuth account after refresh CAS: %w", err)
	}
	if currentAccount == nil {
		return nil, false, errors.New("antigravity OAuth account disappeared after refresh CAS")
	}
	return currentAccount, applied, nil
}

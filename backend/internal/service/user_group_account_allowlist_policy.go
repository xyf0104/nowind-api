package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// ErrUserGroupAccountNotAllowed means an administrator explicitly restricted a
// user/group pair and the requested account is outside that allowlist. It is a
// scheduling boundary, not a transient account-health state.
var ErrUserGroupAccountNotAllowed = errors.New("account is not allowed for this user and group")

// AccountCandidateAccessPolicy is deliberately small so scheduler snapshots
// can enforce an account-selection boundary without depending on admin UI or
// persistence details.
type AccountCandidateAccessPolicy interface {
	FilterCandidates(ctx context.Context, groupID *int64, accounts []Account) ([]Account, error)
	RequireCandidate(ctx context.Context, groupID *int64, accountID int64) error
}

// filterAccountCandidatesWithPolicy keeps the optional policy boundary in one
// place. A persistence failure is intentionally returned to the caller: a
// request must never expand into the unrestricted pool merely because its
// explicit allowlist could not be read.
func filterAccountCandidatesWithPolicy(
	ctx context.Context,
	policy AccountCandidateAccessPolicy,
	groupID *int64,
	accounts []Account,
) ([]Account, error) {
	if policy == nil || len(accounts) == 0 {
		return accounts, nil
	}
	return policy.FilterCandidates(ctx, groupID, accounts)
}

func requireAccountCandidateWithPolicy(
	ctx context.Context,
	policy AccountCandidateAccessPolicy,
	groupID *int64,
	accountID int64,
) error {
	if policy == nil {
		return nil
	}
	return policy.RequireCandidate(ctx, groupID, accountID)
}

// UserGroupAccountAllowlistPolicy applies an optional administrator-selected
// account allowlist to an authenticated API-key request. No stored scope means
// original unrestricted scheduling; a stored scope with no detail rows denies
// every candidate.
type UserGroupAccountAllowlistPolicy struct {
	repository UserGroupAccountAllowlistRepository
}

func NewUserGroupAccountAllowlistPolicy(repository UserGroupAccountAllowlistRepository) *UserGroupAccountAllowlistPolicy {
	return &UserGroupAccountAllowlistPolicy{repository: repository}
}

func (p *UserGroupAccountAllowlistPolicy) FilterCandidates(ctx context.Context, groupID *int64, accounts []Account) ([]Account, error) {
	allowed, restricted, err := p.allowedAccountSet(ctx, groupID)
	if err != nil || !restricted || len(accounts) == 0 {
		return accounts, err
	}

	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if _, ok := allowed[accounts[i].ID]; ok {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered, nil
}

func (p *UserGroupAccountAllowlistPolicy) RequireCandidate(ctx context.Context, groupID *int64, accountID int64) error {
	if accountID <= 0 {
		return fmt.Errorf("%w: invalid account id", ErrUserGroupAccountNotAllowed)
	}
	allowed, restricted, err := p.allowedAccountSet(ctx, groupID)
	if err != nil || !restricted {
		return err
	}
	if _, ok := allowed[accountID]; ok {
		return nil
	}
	return fmt.Errorf("%w: account %d", ErrUserGroupAccountNotAllowed, accountID)
}

func (p *UserGroupAccountAllowlistPolicy) allowedAccountSet(ctx context.Context, groupID *int64) (map[int64]struct{}, bool, error) {
	if p == nil || p.repository == nil || ctx == nil {
		return nil, false, nil
	}
	userID, _ := ctx.Value(ctxkey.UserID).(int64)
	if userID <= 0 {
		return nil, false, nil
	}
	resolvedGroupID := resolveUserGroupAccountAllowlistGroupID(ctx, groupID)
	if resolvedGroupID <= 0 {
		return nil, false, nil
	}
	// ClientRequestID is connection-scoped for WebSockets, so decisions must stay
	// in the repository cache where PUT, DELETE, and cross-instance notices can
	// invalidate them between turns.
	accountIDs, restricted, err := p.repository.GetAllowedAccountIDs(ctx, userID, resolvedGroupID)
	if err != nil {
		return nil, false, err
	}
	allowed := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID > 0 {
			allowed[accountID] = struct{}{}
		}
	}
	return allowed, restricted, nil
}

func resolveUserGroupAccountAllowlistGroupID(ctx context.Context, groupID *int64) int64 {
	if groupID != nil && *groupID > 0 {
		return *groupID
	}
	if ctx == nil {
		return 0
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if !IsGroupContextValid(group) {
		return 0
	}
	return group.ID
}

// userGroupAccountFilteringRepository decorates only candidate-list methods.
// All mutations and non-scheduler reads continue to use the original
// repository implementation unchanged.
type userGroupAccountFilteringRepository struct {
	AccountRepository
	policy AccountCandidateAccessPolicy
}

func NewUserGroupAccountFilteringRepository(repo AccountRepository, policy AccountCandidateAccessPolicy) AccountRepository {
	if repo == nil || policy == nil {
		return repo
	}
	return &userGroupAccountFilteringRepository{AccountRepository: repo, policy: policy}
}

func (r *userGroupAccountFilteringRepository) FilterCandidates(ctx context.Context, groupID *int64, accounts []Account) ([]Account, error) {
	if r == nil || r.policy == nil {
		return accounts, nil
	}
	return r.policy.FilterCandidates(ctx, groupID, accounts)
}

func (r *userGroupAccountFilteringRepository) RequireCandidate(ctx context.Context, groupID *int64, accountID int64) error {
	if r == nil || r.policy == nil {
		return nil
	}
	return r.policy.RequireCandidate(ctx, groupID, accountID)
}

func (r *userGroupAccountFilteringRepository) ListSchedulable(ctx context.Context) ([]Account, error) {
	accounts, err := r.AccountRepository.ListSchedulable(ctx)
	if err != nil {
		return nil, err
	}
	return r.FilterCandidates(ctx, nil, accounts)
}

func (r *userGroupAccountFilteringRepository) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	accounts, err := r.AccountRepository.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return r.FilterCandidates(ctx, &groupID, accounts)
}

func (r *userGroupAccountFilteringRepository) ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error) {
	accounts, err := r.AccountRepository.ListSchedulableByPlatform(ctx, platform)
	if err != nil {
		return nil, err
	}
	return r.FilterCandidates(ctx, nil, accounts)
}

func (r *userGroupAccountFilteringRepository) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	accounts, err := r.AccountRepository.ListSchedulableByGroupIDAndPlatform(ctx, groupID, platform)
	if err != nil {
		return nil, err
	}
	return r.FilterCandidates(ctx, &groupID, accounts)
}

func (r *userGroupAccountFilteringRepository) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	accounts, err := r.AccountRepository.ListSchedulableByPlatforms(ctx, platforms)
	if err != nil {
		return nil, err
	}
	return r.FilterCandidates(ctx, nil, accounts)
}

func (r *userGroupAccountFilteringRepository) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	accounts, err := r.AccountRepository.ListSchedulableByGroupIDAndPlatforms(ctx, groupID, platforms)
	if err != nil {
		return nil, err
	}
	return r.FilterCandidates(ctx, &groupID, accounts)
}

func (r *userGroupAccountFilteringRepository) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]Account, error) {
	accounts, err := r.AccountRepository.ListSchedulableUngroupedByPlatform(ctx, platform)
	if err != nil {
		return nil, err
	}
	return r.FilterCandidates(ctx, nil, accounts)
}

func (r *userGroupAccountFilteringRepository) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	accounts, err := r.AccountRepository.ListSchedulableUngroupedByPlatforms(ctx, platforms)
	if err != nil {
		return nil, err
	}
	return r.FilterCandidates(ctx, nil, accounts)
}

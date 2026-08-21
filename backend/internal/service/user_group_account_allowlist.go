package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrUserGroupAccountAllowlistInvalidID   = errors.New("user group account allowlist contains an invalid account id")
	ErrUserGroupAccountAllowlistUnavailable = errors.New("user group account allowlist contains an unavailable account")
)

// UserGroupAccountAllowlistRepository persists optional user x group account
// restrictions. A missing scope deliberately means unrestricted scheduling;
// a present scope may contain zero selected accounts and remains restricted.
type UserGroupAccountAllowlistRepository interface {
	GetAllowedAccountIDs(ctx context.Context, userID, groupID int64) (accountIDs []int64, restricted bool, err error)
	ReplaceAllowedAccountIDs(ctx context.Context, userID, groupID int64, accountIDs []int64) error
	RestoreAllowedAccountIDs(ctx context.Context, userID, groupID int64) error
}

// UserGroupAccountAllowlistCandidateRepository supplies the current account
// pool shown to administrators. AccountRepository satisfies this interface.
type UserGroupAccountAllowlistCandidateRepository interface {
	ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error)
	GetByIDs(ctx context.Context, ids []int64) ([]*Account, error)
}

// UserGroupAccountAllowlistCandidate is an account that may currently be
// selected for a user's optional allowlist.
type UserGroupAccountAllowlistCandidate struct {
	AccountID   int64
	Name        string
	Platform    string
	Type        string
	Priority    int
	Concurrency int
	Allowed     bool
	Available   bool
}

// UserGroupAccountAllowlistSelection is the admin read model. Configured
// accounts remain in Candidates even when they are currently unavailable.
type UserGroupAccountAllowlistSelection struct {
	Restricted        bool
	AllowedAccountIDs []int64
	Candidates        []UserGroupAccountAllowlistCandidate
}

// UserGroupAccountAllowlistService owns validation for admin writes and the
// read model used by the future account-selection UI. It deliberately has no
// gateway or scheduler dependency.
type UserGroupAccountAllowlistService struct {
	repository UserGroupAccountAllowlistRepository
	candidates UserGroupAccountAllowlistCandidateRepository
}

func NewUserGroupAccountAllowlistService(
	repository UserGroupAccountAllowlistRepository,
	candidates UserGroupAccountAllowlistCandidateRepository,
) *UserGroupAccountAllowlistService {
	return &UserGroupAccountAllowlistService{
		repository: repository,
		candidates: candidates,
	}
}

func (s *UserGroupAccountAllowlistService) GetAllowedAccountIDs(
	ctx context.Context,
	userID, groupID int64,
) ([]int64, bool, error) {
	if err := validateUserGroupAccountAllowlistScope(userID, groupID); err != nil {
		return nil, false, err
	}
	if s == nil || s.repository == nil {
		return nil, false, errors.New("nil user group account allowlist repository")
	}
	return s.repository.GetAllowedAccountIDs(ctx, userID, groupID)
}

func (s *UserGroupAccountAllowlistService) GetAdminSelection(
	ctx context.Context,
	userID, groupID int64,
) (*UserGroupAccountAllowlistSelection, error) {
	if err := validateUserGroupAccountAllowlistScope(userID, groupID); err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil || s.candidates == nil {
		return nil, errors.New("nil user group account allowlist dependency")
	}

	allowedIDs, restricted, err := s.repository.GetAllowedAccountIDs(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	allowed := int64Set(allowedIDs)

	accounts, err := s.candidates.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	candidates := make([]UserGroupAccountAllowlistCandidate, 0, len(accounts)+len(allowedIDs))
	availableIDs := make(map[int64]struct{}, len(accounts))
	for i := range accounts {
		account := accounts[i]
		if !account.IsSchedulable() {
			continue
		}
		availableIDs[account.ID] = struct{}{}
		candidates = append(candidates, UserGroupAccountAllowlistCandidate{
			AccountID:   account.ID,
			Name:        account.Name,
			Platform:    account.Platform,
			Type:        account.Type,
			Priority:    account.Priority,
			Concurrency: account.Concurrency,
			Allowed:     allowed[account.ID],
			Available:   true,
		})
	}

	unavailableIDs := make([]int64, 0, len(allowedIDs))
	for _, accountID := range allowedIDs {
		if _, available := availableIDs[accountID]; !available {
			unavailableIDs = append(unavailableIDs, accountID)
		}
	}
	if len(unavailableIDs) > 0 {
		storedAccounts, err := s.candidates.GetByIDs(ctx, unavailableIDs)
		if err != nil {
			return nil, err
		}
		sort.Slice(storedAccounts, func(i, j int) bool {
			if storedAccounts[i] == nil {
				return false
			}
			if storedAccounts[j] == nil {
				return true
			}
			return storedAccounts[i].ID < storedAccounts[j].ID
		})
		for _, account := range storedAccounts {
			if account == nil || !allowed[account.ID] {
				continue
			}
			if _, available := availableIDs[account.ID]; available {
				continue
			}
			candidates = append(candidates, UserGroupAccountAllowlistCandidate{
				AccountID:   account.ID,
				Name:        account.Name,
				Platform:    account.Platform,
				Type:        account.Type,
				Priority:    account.Priority,
				Concurrency: account.Concurrency,
				Allowed:     true,
				Available:   false,
			})
		}
	}

	return &UserGroupAccountAllowlistSelection{
		Restricted:        restricted,
		AllowedAccountIDs: normalizeUserGroupAccountIDs(allowedIDs),
		Candidates:        candidates,
	}, nil
}

// ReplaceAllowedAccountIDs stores the explicit allowed set. An empty set keeps
// the scope restricted with zero candidates. Existing unavailable selections
// may be retained, while newly selected accounts must currently be schedulable.
func (s *UserGroupAccountAllowlistService) ReplaceAllowedAccountIDs(
	ctx context.Context,
	userID, groupID int64,
	accountIDs []int64,
) error {
	if err := validateUserGroupAccountAllowlistScope(userID, groupID); err != nil {
		return err
	}
	if s == nil || s.repository == nil {
		return errors.New("nil user group account allowlist repository")
	}

	normalized, err := normalizeAndValidateUserGroupAccountIDs(accountIDs)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return s.repository.ReplaceAllowedAccountIDs(ctx, userID, groupID, []int64{})
	}
	if s.candidates == nil {
		return errors.New("nil user group account allowlist candidate repository")
	}
	existingIDs, _, err := s.repository.GetAllowedAccountIDs(ctx, userID, groupID)
	if err != nil {
		return err
	}
	existing := int64Set(existingIDs)

	accounts, err := s.candidates.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return err
	}
	available := make(map[int64]struct{}, len(accounts))
	for i := range accounts {
		account := accounts[i]
		if account.IsSchedulable() {
			available[account.ID] = struct{}{}
		}
	}
	for _, accountID := range normalized {
		if _, ok := available[accountID]; !ok && !existing[accountID] {
			return fmt.Errorf("%w: %d", ErrUserGroupAccountAllowlistUnavailable, accountID)
		}
	}

	return s.repository.ReplaceAllowedAccountIDs(ctx, userID, groupID, normalized)
}

func (s *UserGroupAccountAllowlistService) RestoreAllowedAccountIDs(
	ctx context.Context,
	userID, groupID int64,
) error {
	if err := validateUserGroupAccountAllowlistScope(userID, groupID); err != nil {
		return err
	}
	if s == nil || s.repository == nil {
		return errors.New("nil user group account allowlist repository")
	}
	return s.repository.RestoreAllowedAccountIDs(ctx, userID, groupID)
}

func validateUserGroupAccountAllowlistScope(userID, groupID int64) error {
	if userID <= 0 || groupID <= 0 {
		return errors.New("user id and group id must be positive")
	}
	return nil
}

func normalizeAndValidateUserGroupAccountIDs(accountIDs []int64) ([]int64, error) {
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			return nil, fmt.Errorf("%w: %d", ErrUserGroupAccountAllowlistInvalidID, accountID)
		}
	}
	return normalizeUserGroupAccountIDs(accountIDs), nil
}

func normalizeUserGroupAccountIDs(accountIDs []int64) []int64 {
	if len(accountIDs) == 0 {
		return []int64{}
	}
	unique := int64Set(accountIDs)
	normalized := make([]int64, 0, len(unique))
	for accountID := range unique {
		normalized = append(normalized, accountID)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func int64Set(values []int64) map[int64]bool {
	result := make(map[int64]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

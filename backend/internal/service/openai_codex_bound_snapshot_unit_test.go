//go:build unit

package service

import "context"

// Extend the existing rate-limit shadow fixture without editing its admin-owned file.
func (r *updateExtraSpyRepo) UpdateOpenAICodexSnapshotIfIdentityMatches(_ context.Context, _ *Account, _ map[string]any) (bool, error) {
	r.updateExtraCalled = true
	return true, nil
}

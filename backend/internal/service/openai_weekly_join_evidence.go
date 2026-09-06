package service

import (
	"context"
	"errors"
	"time"
)

var (
	ErrOpenAIWeeklyJoinEvidenceInvalidInput     = errors.New("invalid OpenAI weekly join evidence input")
	ErrOpenAIWeeklyJoinEvidenceSnapshotRequired = errors.New("OpenAI weekly join evidence requires a read-only repeatable-read or serializable transaction")
)

type OpenAIWeeklyJoinEvidenceKind string

const (
	OpenAIWeeklyJoinEvidenceLocalInception        OpenAIWeeklyJoinEvidenceKind = "local_nested_zero_cost_inception"
	OpenAIWeeklyJoinEvidenceImportedCorroboration OpenAIWeeklyJoinEvidenceKind = "imported_baseline_local_raw_corroboration"
)

// OpenAIWeeklyJoinEvidence is historical evidence, not an instruction to import
// state or an estimate. Cost is account USD, never the user's billed amount.
// ObservedAt is always local (at/after account creation). BaselineObservedAt is
// equal to ObservedAt for LocalInception, but predates local account creation for
// ImportedCorroboration. AuditCreatedAt may be later than either observation.
type OpenAIWeeklyJoinEvidence struct {
	AccountID          int64
	Identity           string
	Kind               OpenAIWeeklyJoinEvidenceKind
	Reason             string
	AuditID            int64
	AuditCreatedAt     time.Time
	BaselineObservedAt time.Time
	ObservedAt         time.Time
	ResetAt            time.Time
	Percent            float64
	Cost               float64
	VerifiedAt         time.Time
}

// OpenAIWeeklyJoinEvidenceRepository is optional; AccountRepository is unchanged.
// Nil evidence with nil error means the bounded retained history cannot prove an
// unambiguous zero-cost join in the current week. The resolver never writes state.
// The concrete resolver accepts at most 256 per-account update audits with bodies
// at most 16 KiB, within a five-second query budget and 60-second reset tolerance.
// Imported corroboration requires an explicit identity on the same raw snapshot;
// generic bulk imports and missing/redacted identity are not adoption evidence.
// Callers must still CAS the current account snapshot before adopting evidence;
// this read cannot protect against later billing, audit retention or backfills.
// An ambient ent transaction must already be read-only repeatable-read (or
// serializable); its commit/rollback remains the caller's responsibility.
type OpenAIWeeklyJoinEvidenceRepository interface {
	FindOpenAIWeeklyJoinEvidence(ctx context.Context, expected *Account, currentResetAt time.Time) (*OpenAIWeeklyJoinEvidence, error)
}

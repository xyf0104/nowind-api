package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
)

const OpenAIWeeklyStateRevisionKey = "codex_7d_estimate_revision"

// Extra is decoded through float64 by legacy readers. Keep counters exact there.
const openAIWeeklyStateMaxRevision int64 = 1<<53 - 1

func openAIWeeklyStateInteger(value any) (int64, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, false
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return 0, false
	}
	number, ok := raw.(json.Number)
	if !ok {
		return 0, false
	}
	rational, ok := new(big.Rat).SetString(number.String())
	if !ok || !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, false
	}
	integer := rational.Num().Int64()
	return integer, integer >= 0 && integer <= openAIWeeklyStateMaxRevision
}

func openAIWeeklyStateVersion(value any) int64 {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(encoded, &raw) != nil {
		return 0
	}
	version, _ := openAIWeeklyStateInteger(raw["version"])
	return version
}

// PrepareOpenAIWeeklyStateUpdate allocates a revision from the ORIGINAL CAS
// snapshot, not an intermediate historical seed. Reapplying it is idempotent.
// Neither input is mutated. Opaque pre-v15 CAS callers retain their old format.
func PrepareOpenAIWeeklyStateUpdate(expectedExtra, updates map[string]any) (map[string]any, error) {
	if len(updates) == 0 {
		return updates, nil
	}
	current, present := expectedExtra[OpenAIWeeklyStateRevisionKey]
	fenced := present || openAIWeeklyStateVersion(expectedExtra[openAIWeeklyEstimateBaselineKey]) >= 15 ||
		openAIWeeklyStateVersion(updates[openAIWeeklyEstimateBaselineKey]) >= 15
	if !fenced {
		if _, supplied := updates[OpenAIWeeklyStateRevisionKey]; supplied {
			return nil, fmt.Errorf("%w: revision requires a v15 state", ErrOpenAIWeeklyStateInvalidInput)
		}
		return maps.Clone(updates), nil
	}
	revision := int64(0)
	if present {
		var valid bool
		revision, valid = openAIWeeklyStateInteger(current)
		if !valid || revision >= openAIWeeklyStateMaxRevision {
			return nil, fmt.Errorf("%w: invalid or exhausted state revision", ErrOpenAIWeeklyStateInvalidInput)
		}
	}
	next := revision + 1
	if supplied, ok := updates[OpenAIWeeklyStateRevisionKey]; ok {
		value, valid := openAIWeeklyStateInteger(supplied)
		if !valid || value != next {
			return nil, fmt.Errorf("%w: revision does not follow the original snapshot", ErrOpenAIWeeklyStateInvalidInput)
		}
	}
	patch := maps.Clone(updates)
	patch[OpenAIWeeklyStateRevisionKey] = next
	return patch, nil
}

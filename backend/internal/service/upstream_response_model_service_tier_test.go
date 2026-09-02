package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamResponseModelObserverCapturesServiceTier(t *testing.T) {
	observer := &upstreamResponseModelObserver{}
	observer.ObserveOpenAI(
		[]byte(`{"type":"response.completed","response":{"model":"gpt-5.6-sol","service_tier":"default"}}`),
		"response.completed",
	)
	require.Equal(t, "default", observer.ServiceTier())

	observer = &upstreamResponseModelObserver{}
	observer.ObserveAnthropic(
		[]byte(`{"message":{"model":"claude-fable-5-1","usage":{"speed":"fast"}}}`),
	)
	require.Equal(t, "fast", observer.ServiceTier())
}

func TestUpstreamResponseModelObserverRejectsConflictingNonTerminalTier(t *testing.T) {
	observer := &upstreamResponseModelObserver{}
	observer.ObserveServiceTier("standard", false)
	observer.ObserveServiceTier("fast", false)
	require.Empty(t, observer.ServiceTier())
}

//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSIngressEndedByClient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "bare normal closure", err: coderws.CloseError{Code: coderws.StatusNormalClosure, Reason: "client done"}, want: true},
		{name: "wrapped normal closure", err: fmt.Errorf("turn: %w", coderws.CloseError{Code: coderws.StatusNormalClosure}), want: true},
		{name: "gateway normal closure", err: service.NewOpenAIWSClientCloseError(coderws.StatusNormalClosure, "idle", context.DeadlineExceeded), want: true},
		{name: "client cancellation", err: service.NewOpenAIWSClientCloseError(coderws.StatusGoingAway, "cancelled", context.Canceled), want: true},
		{name: "upstream policy failure", err: service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "upstream rejected", errors.New("credential failure")), want: false},
		{name: "upstream reset", err: coderws.CloseError{Code: coderws.StatusAbnormalClosure, Reason: "connection reset"}, want: false},
		{name: "upstream deadline", err: fmt.Errorf("upstream stalled: %w", context.DeadlineExceeded), want: false},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, openAIWSIngressEndedByClient(test.err))
			if !test.want {
				require.True(t, shouldReportOpenAIWSProxyAccountFailure(test.err))
			}
		})
	}
}

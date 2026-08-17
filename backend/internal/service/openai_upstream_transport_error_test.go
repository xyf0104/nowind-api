//go:build unit

package service

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
)

// TestClassifyOpenAITransportError pins explicit proxy credential failures as
// persistent while every connectivity failure remains request-local. Router/WAN
// restarts commonly surface as connection refused, no route or DNS failures.
//
// The motivating incident: a SOCKS5 proxy whose credentials expired returned
// `username/password authentication failed`, yet the account kept being scheduled.
func TestClassifyOpenAITransportError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		persistent bool
	}{
		// Durable — explicit proxy credential/configuration failures.
		{"socks5 proxy credential rejected", errors.New(`Post "https://chatgpt.com/backend-api/codex/responses": socks connect tcp 85.255.176.68:12324->chatgpt.com:443: username/password authentication failed`), true},
		{"http proxy credential rejected", errors.New(`proxy authentication required`), true},

		// Connectivity — fail over, but never persistently evict the account.
		{"proxy connection refused", errors.New(`proxyconnect tcp: dial tcp 1.2.3.4:1080: connect: connection refused`), false},
		{"no route to host", errors.New(`dial tcp 1.2.3.4:443: connect: no route to host`), false},
		{"dns resolution failure", errors.New(`dial tcp: lookup proxy.example.com: no such host`), false},
		{"network unreachable", errors.New(`dial tcp 1.2.3.4:443: connect: network is unreachable`), false},
		{"client timeout", errors.New(`Post "https://chatgpt.com/...": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`), false},
		{"i/o timeout", errors.New(`dial tcp 1.2.3.4:443: i/o timeout`), false},
		{"connection reset by peer", errors.New(`read tcp 10.0.0.1:5->2.2.2.2:443: read: connection reset by peer`), false},
		{"unexpected eof", errors.New(`unexpected EOF`), false},
		{"broken pipe", errors.New(`write tcp 10.0.0.1:5->2.2.2.2:443: write: broken pipe`), false},

		{"nil error", nil, false},

		// ── Typed-error cases ──────────────────────────────────────────────
		// Typed connectivity errors remain transient as well.
		{
			"ECONNREFUSED via net.OpError",
			&net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
			},
			false,
		},
		{"ECONNREFUSED bare", syscall.ECONNREFUSED, false},
		{"EHOSTUNREACH bare", syscall.EHOSTUNREACH, false},
		{"ENETUNREACH bare", syscall.ENETUNREACH, false},

		// *net.DNSError with IsNotFound — permanent DNS lookup failure.
		{
			"DNS not found (IsNotFound=true)",
			&net.DNSError{Err: "no such host", Name: "proxy.example.com", IsNotFound: true},
			false,
		},
		// *net.DNSError with IsNotFound=false — transient DNS timeout (not persistent).
		{
			"DNS timeout (IsNotFound=false)",
			&net.DNSError{Err: "i/o timeout", Name: "proxy.example.com", IsTimeout: true},
			false,
		},

		// context.Canceled — client gone; NOT classified as persistent.
		{"context.Canceled", context.Canceled, false},
		// context.DeadlineExceeded — slow upstream; NOT persistent.
		{"context.DeadlineExceeded", context.DeadlineExceeded, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOpenAITransportError(tc.err).Persistent
			if got != tc.persistent {
				t.Fatalf("classifyOpenAITransportError(%q).Persistent = %v, want %v", errString(tc.err), got, tc.persistent)
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

package service

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

// batchImageAccountProxyURL resolves the durable egress attached to an account.
// A missing proxy is valid for legacy single-node accounts, but a dangling or
// inactive proxy ID must never silently turn into a direct request.
func batchImageAccountProxyURL(account *Account) (string, error) {
	if account == nil || account.ProxyID == nil {
		return "", nil
	}
	if *account.ProxyID <= 0 || account.Proxy == nil || account.Proxy.ID != *account.ProxyID || !account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now()) {
		return "", ErrBatchImageProviderEgressUnavailable
	}
	return account.requestProxyURL(), nil
}

func newBatchImageHTTPClient(proxyURL string) (*http.Client, error) {
	return httpclient.GetClient(httpclient.Options{
		ProxyURL:              strings.TrimSpace(proxyURL),
		ResponseHeaderTimeout: 60 * time.Second,
	})
}

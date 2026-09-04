//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewBatchImageHTTPClientUsesConfiguredHTTPProxy(t *testing.T) {
	originCalled := false
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		originCalled = true
		http.Error(w, "direct request should not reach origin", http.StatusInternalServerError)
	}))
	defer origin.Close()

	proxyTarget := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyTarget <- r.URL.String()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()

	client, err := newBatchImageHTTPClient(proxy.URL)
	require.NoError(t, err)
	resp, err := client.Get(origin.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, origin.URL+"/", <-proxyTarget)
	require.False(t, originCalled)
}

func TestBatchImageAccountProxyURLRequiresHydratedActiveProxy(t *testing.T) {
	proxyID := int64(17)
	account := &Account{ProxyID: &proxyID}

	_, err := batchImageAccountProxyURL(account)
	require.ErrorIs(t, err, ErrBatchImageProviderEgressUnavailable)

	account.Proxy = &Proxy{ID: proxyID, Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: StatusExpired}
	_, err = batchImageAccountProxyURL(account)
	require.ErrorIs(t, err, ErrBatchImageProviderEgressUnavailable)

	account.Proxy.Status = StatusActive
	proxyURL, err := batchImageAccountProxyURL(account)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8080", proxyURL)
}

func TestGeminiBatchProviderDefaultFactoryUsesAccountProxy(t *testing.T) {
	provider := NewGeminiAPIBatchImageProvider(nil)
	var gotProxyURL string
	client := &fakeGeminiBatchClient{}
	provider.clientFactory = func(proxyURL string) (GeminiBatchClient, error) {
		gotProxyURL = proxyURL
		return client, nil
	}

	proxyID := int64(23)
	account := geminiAPIKeyAccount("sk-gemini")
	account.ProxyID = &proxyID
	account.Proxy = &Proxy{ID: proxyID, Protocol: "socks5", Host: "proxy.internal", Port: 1080, Status: StatusActive}

	_, err := provider.Submit(context.Background(), nil, account, validGeminiBatchInput())
	require.NoError(t, err)
	require.Equal(t, "socks5://proxy.internal:1080", gotProxyURL)
}

func TestVertexBatchProviderDefaultFactoriesUseAccountProxy(t *testing.T) {
	provider := newTestVertexProvider(nil, nil)
	var gotProxyURL []string
	client := &fakeVertexBatchClient{}
	store := &fakeVertexObjectStore{}
	provider.clientFactory = func(proxyURL string) (VertexBatchClient, error) {
		gotProxyURL = append(gotProxyURL, "client:"+proxyURL)
		return client, nil
	}
	provider.objectStoreFactory = func(proxyURL string) (VertexBatchObjectStore, error) {
		gotProxyURL = append(gotProxyURL, "store:"+proxyURL)
		return store, nil
	}

	proxyID := int64(29)
	account := vertexServiceAccount()
	account.ProxyID = &proxyID
	account.Proxy = &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.internal", Port: 8080, Status: StatusActive}

	_, err := provider.Submit(context.Background(), &BatchImageJob{BatchID: "imgbatch_proxy", Model: "gemini-3.1-flash-image"}, account, validGeminiBatchInput())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"client:http://proxy.internal:8080",
		"store:http://proxy.internal:8080",
	}, gotProxyURL)
}

func TestBatchImageProviderDoesNotUseInjectedClientForDanglingAccountProxy(t *testing.T) {
	gemini := NewGeminiAPIBatchImageProvider(&fakeGeminiBatchClient{})
	proxyID := int64(31)
	account := geminiAPIKeyAccount("sk-gemini")
	account.ProxyID = &proxyID

	_, err := gemini.Submit(context.Background(), nil, account, validGeminiBatchInput())
	require.ErrorIs(t, err, ErrBatchImageProviderEgressUnavailable)

	vertex := newTestVertexProvider(&fakeVertexBatchClient{}, &fakeVertexObjectStore{})
	vertexAccount := vertexServiceAccount()
	vertexAccount.ProxyID = &proxyID
	_, err = vertex.Submit(context.Background(), &BatchImageJob{BatchID: "imgbatch_proxy_missing", Model: "gemini-3.1-flash-image"}, vertexAccount, validGeminiBatchInput())
	require.ErrorIs(t, err, ErrBatchImageProviderEgressUnavailable)
}

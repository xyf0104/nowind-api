package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSendCCUpstreamRequestFreezesProxyForEveryAccountAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{err: errors.New("dial failed")}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	proxyID := int64(71)
	proxied := &Account{
		ID: 101, Name: "proxied", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		ProxyID: &proxyID,
		Proxy:   &Proxy{ID: proxyID, Name: "proxy-a", Protocol: "http", Host: "127.0.0.1", Port: 18080},
	}
	direct := &Account{ID: 202, Name: "direct", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1}

	_, err := svc.sendCCUpstreamRequest(context.Background(), c, proxied, "https://upstream.example/v1/chat/completions", []byte(`{"model":"gpt-5"}`), false, "sk-test", "", "")
	require.Error(t, err)
	firstSnapshot := currentOpsUpstreamProxySnapshot(c)
	require.Equal(t, opsUpstreamProxyManaged, firstSnapshot.mode)
	require.Equal(t, proxyID, firstSnapshot.proxyID)
	require.Equal(t, "proxy-a", firstSnapshot.proxyName)

	_, err = svc.sendCCUpstreamRequest(context.Background(), c, direct, "https://upstream.example/v1/chat/completions", []byte(`{"model":"gpt-5"}`), false, "sk-test", "", "")
	require.Error(t, err)
	secondSnapshot := currentOpsUpstreamProxySnapshot(c)
	require.Equal(t, opsUpstreamProxyDirect, secondSnapshot.mode)
	require.Zero(t, secondSnapshot.proxyID)
	require.Equal(t, opsProxyNameDirect, secondSnapshot.proxyName)

	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].ProxyID)
	require.Equal(t, proxyID, *events[0].ProxyID)
	require.Equal(t, "proxy-a", events[0].ProxyName)
	require.Nil(t, events[1].ProxyID)
	require.Equal(t, opsProxyNameDirect, events[1].ProxyName)
}

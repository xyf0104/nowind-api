package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeOpsUpstreamErrorsForQueueBoundsAndRedacts(t *testing.T) {
	entry := &OpsInsertErrorLogInput{}
	for i := 0; i < 20; i++ {
		proxyID := int64(i + 1)
		entry.UpstreamErrors = append(entry.UpstreamErrors, &OpsUpstreamErrorEvent{
			ProxyID:              &proxyID,
			ProxyName:            "proxy",
			Platform:             strings.Repeat("p", 100),
			AccountName:          strings.Repeat("a", 300),
			UpstreamStatusCode:   500,
			UpstreamURL:          strings.Repeat("u", 3000),
			UpstreamResponseBody: `{"authorization":"Bearer secret","message":"` + strings.Repeat("x", 10_000) + `"}`,
			Message:              strings.Repeat("m", 3000),
			Detail:               `{"api_key":"secret","detail":"` + strings.Repeat("y", 10_000) + `"}`,
		})
	}

	if err := SanitizeOpsUpstreamErrorsForQueue(entry); err != nil {
		t.Fatal(err)
	}
	if entry.UpstreamErrors != nil {
		t.Fatal("raw upstream event slice must be released before queueing")
	}
	if entry.UpstreamErrorsJSON == nil {
		t.Fatal("sanitized upstream event JSON is missing")
	}
	events, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 20 {
		t.Fatalf("event count = %d, want 20", len(events))
	}
	for i, event := range events {
		if len(event.Platform) > 32 || len(event.AccountName) > 128 || len(event.UpstreamURL) > 2048 || len(event.Message) > 2048 {
			t.Fatalf("event fields were not bounded: %+v", event)
		}
		if i < 4 && (event.UpstreamResponseBody != "" || event.Detail != "") {
			t.Fatal("events outside the body window retained large payload fields")
		}
		if len(event.UpstreamResponseBody) > OpsErrorLogQueueBodyMaxBytes || len(event.Detail) > OpsErrorLogQueueBodyMaxBytes {
			t.Fatal("event body/detail exceeded queue limit")
		}
		if strings.Contains(event.UpstreamResponseBody, "Bearer secret") || strings.Contains(event.Detail, `"secret"`) {
			t.Fatal("credential material was not redacted")
		}
	}
}

func TestSanitizeOpsUpstreamErrorsForQueueHardCountLimitAndDropCounter(t *testing.T) {
	entry := &OpsInsertErrorLogInput{}
	total := opsUpstreamErrorsMaxEvents + 40
	for i := 0; i < total; i++ {
		entry.UpstreamErrors = append(entry.UpstreamErrors, &OpsUpstreamErrorEvent{
			AtUnixMs:           int64(i + 1),
			ProxyName:          opsProxyNameDirect,
			UpstreamStatusCode: 500,
			Message:            "attempt",
		})
	}

	require.NoError(t, SanitizeOpsUpstreamErrorsForQueue(entry))
	require.NotNil(t, entry.UpstreamErrorsJSON)
	events, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, events, opsUpstreamErrorsMaxEvents)
	require.Equal(t, 40, events[0].DroppedEarlierAttempts)
	require.Equal(t, int64(total), events[len(events)-1].AtUnixMs)
}

func TestSanitizeOpsUpstreamErrorsForQueueAccumulatesExistingAndNewDrops(t *testing.T) {
	entry := &OpsInsertErrorLogInput{}
	total := opsUpstreamErrorsMaxEvents + 4
	for i := 0; i < total; i++ {
		event := &OpsUpstreamErrorEvent{
			AtUnixMs:           int64(i + 1),
			ProxyName:          opsProxyNameDirect,
			UpstreamStatusCode: 500,
			Message:            "attempt",
		}
		if i == 0 {
			event.DroppedEarlierAttempts = 7
		}
		entry.UpstreamErrors = append(entry.UpstreamErrors, event)
	}

	require.NoError(t, SanitizeOpsUpstreamErrorsForQueue(entry))
	require.NotNil(t, entry.UpstreamErrorsJSON)
	events, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, events, opsUpstreamErrorsMaxEvents)
	require.Equal(t, 11, events[0].DroppedEarlierAttempts)
	require.Equal(t, int64(total), events[len(events)-1].AtUnixMs)
}

func TestSanitizeOpsUpstreamErrorsForQueueRedactsProxyNameAndURLUserinfo(t *testing.T) {
	proxyID := int64(22)
	entry := &OpsInsertErrorLogInput{UpstreamErrors: []*OpsUpstreamErrorEvent{{
		ProxyID:            &proxyID,
		ProxyName:          "socks5://proxy-user:proxy-password@198.51.100.22:1080",
		UpstreamStatusCode: 502,
		UpstreamURL:        "https://upstream-user:upstream-password@api.example.com/v1/responses?token=secret#fragment",
		Message:            "failed",
	}}}

	require.NoError(t, SanitizeOpsUpstreamErrorsForQueue(entry))
	require.NotNil(t, entry.UpstreamErrorsJSON)
	events, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].ProxyID)
	require.Equal(t, proxyID, *events[0].ProxyID)
	require.Equal(t, opsProxyNameUnnamed, events[0].ProxyName)
	require.Equal(t, "https://api.example.com/v1/responses", events[0].UpstreamURL)
	for _, sensitive := range []string{"proxy-user", "proxy-password", "198.51.100.22", "upstream-user", "upstream-password", "token=secret"} {
		require.NotContains(t, *entry.UpstreamErrorsJSON, sensitive)
	}
}

func TestSanitizeOpsUpstreamErrorsForQueueHardSerializedByteLimit(t *testing.T) {
	entry := &OpsInsertErrorLogInput{}
	for i := 0; i < opsUpstreamErrorsMaxEvents; i++ {
		entry.UpstreamErrors = append(entry.UpstreamErrors, &OpsUpstreamErrorEvent{
			AtUnixMs:             int64(i + 1),
			ProxyName:            opsProxyNameDirect,
			UpstreamStatusCode:   502,
			UpstreamURL:          "https://example.com/" + strings.Repeat("u", 3000),
			UpstreamResponseBody: strings.Repeat("b", 10_000),
			Message:              strings.Repeat("m", 3000),
			Detail:               strings.Repeat("d", 10_000),
		})
	}

	require.NoError(t, SanitizeOpsUpstreamErrorsForQueue(entry))
	require.NotNil(t, entry.UpstreamErrorsJSON)
	require.LessOrEqual(t, len(*entry.UpstreamErrorsJSON), opsUpstreamErrorsQueueMaxBytes)
	events, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	require.Less(t, len(events), opsUpstreamErrorsMaxEvents)
	require.Equal(t, opsUpstreamErrorsMaxEvents-len(events), events[0].DroppedEarlierAttempts)
	require.Equal(t, int64(opsUpstreamErrorsMaxEvents), events[len(events)-1].AtUnixMs)
}

func TestBoundOpsUpstreamErrorsDropsSingleOversizedEvent(t *testing.T) {
	events, dropped := boundOpsUpstreamErrors([]*OpsUpstreamErrorEvent{{
		Reason: strings.Repeat("r", opsUpstreamErrorsQueueMaxBytes),
	}})

	require.Empty(t, events)
	require.Equal(t, 1, dropped)
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCodexBootstrapNormalizersPreserveOrdinaryMissingCallIDValidation(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"other","output":"ordinary tool output"}]}`)

	automationBody, automationChanged := normalizeCodexAutomationBootstrap(body)
	require.False(t, automationChanged)
	require.Equal(t, body, automationBody)

	delegationBody, delegationChanged := normalizeCodexDelegationBootstrap(automationBody)
	require.False(t, delegationChanged)
	require.Equal(t, body, delegationBody)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	handler := &OpenAIGatewayHandler{}

	require.False(t, handler.validateFunctionCallOutputRequest(context, delegationBody, zap.NewNop()))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

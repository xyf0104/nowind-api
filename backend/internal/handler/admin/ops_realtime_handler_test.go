package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsRealtimeHandlersDisableResponseCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOpsHandler(nil)

	tests := []struct {
		name   string
		handle gin.HandlerFunc
	}{
		{name: "account concurrency", handle: h.GetConcurrencyStats},
		{name: "user concurrency", handle: h.GetUserConcurrencyStats},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			tt.handle(context)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Equal(t, "private, no-store, max-age=0", recorder.Header().Get("Cache-Control"))
			require.Equal(t, "no-cache", recorder.Header().Get("Pragma"))
			require.Equal(t, "0", recorder.Header().Get("Expires"))
		})
	}
}

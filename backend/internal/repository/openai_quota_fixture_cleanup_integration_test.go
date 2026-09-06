//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The harness is shared with proxy listing/count tests. These fixtures commit
// outside a test transaction, so remove only their own proxy and references.
func cleanupOpenAIQuotaTestProxy(t *testing.T, id int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET proxy_id=NULL WHERE proxy_id=$1`, id)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM proxies WHERE id=$1`, id)
		require.NoError(t, err)
	})
}

package payment

import (
	"testing"
	"time"

	"github.com/midtrans/midtrans-go"
	"github.com/stretchr/testify/require"
)

func TestNewMidtransGatewayDisablesSecretDebugLogging(t *testing.T) {
	gw := NewMidtransGateway("SB-Mid-server-secret", false)

	snapHTTP, ok := gw.snap.HttpClient.(*midtrans.HttpClientImplementation)
	require.True(t, ok)
	snapLog, ok := snapHTTP.Logger.(*midtrans.LoggerImplementation)
	require.True(t, ok)
	require.Equal(t, midtrans.NoLogging, snapLog.LogLevel, "logger bawaan midtrans-go wajib dimatikan agar Basic Auth ServerKey tidak bocor ke stdout")
	require.Equal(t, 15*time.Second, snapHTTP.HttpClient.Timeout)

	coreHTTP, ok := gw.core.HttpClient.(*midtrans.HttpClientImplementation)
	require.True(t, ok)
	coreLog, ok := coreHTTP.Logger.(*midtrans.LoggerImplementation)
	require.True(t, ok)
	require.Equal(t, midtrans.NoLogging, coreLog.LogLevel)
	require.Equal(t, 15*time.Second, coreHTTP.HttpClient.Timeout)
}

package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ticent/internal/platform/httpx"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestIDGeneratesAndPreservesHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(httpx.RequestID())
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"request_id": httpx.RequestIDFrom(c)})
	})

	// 1) Tanpa header masuk -> server membuatkan ID unik dan menaruhnya di header + context.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(rec1, req1)
	genID := rec1.Header().Get(httpx.HeaderRequestID)
	require.NotEmpty(t, genID)
	require.JSONEq(t, `{"request_id":"`+genID+`"}`, rec1.Body.String())

	// 2) Dengan header X-Request-ID masuk -> nilai yang sama dipertahankan untuk pelacakan end-to-end.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req2.Header.Set(httpx.HeaderRequestID, "req-war-001")
	r.ServeHTTP(rec2, req2)
	require.Equal(t, "req-war-001", rec2.Header().Get(httpx.HeaderRequestID))
	require.JSONEq(t, `{"request_id":"req-war-001"}`, rec2.Body.String())

	// 3) Karakter tidak aman diganti ID baru supaya log tetap bersih.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req3.Header.Set(httpx.HeaderRequestID, "bad<script>id")
	r.ServeHTTP(rec3, req3)
	safeID := rec3.Header().Get(httpx.HeaderRequestID)
	require.NotEmpty(t, safeID)
	require.NotEqual(t, "bad<script>id", safeID)
}

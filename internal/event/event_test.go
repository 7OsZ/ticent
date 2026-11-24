package event_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ticent/internal/event"
	"ticent/internal/platform/testutil"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateEventSessionMinutesValidation(t *testing.T) {
	db := testutil.DB(t)
	svc := event.NewService(event.NewRepository(db))

	sessions200 := 200
	minutes30 := 30
	ev, err := svc.CreateEvent(event.CreateEventInput{
		Name:              "BMTH Live in Jakarta",
		Venue:             "GBK",
		SaleStartAt:       time.Now().Add(time.Hour),
		MaxActiveSessions: &sessions200,
		SessionMinutes:    &minutes30,
	})
	require.NoError(t, err, "max_active_sessions 200 (1..1000) dan session_minutes 30 (5..120) harus lolos")
	require.Equal(t, 200, ev.MaxActiveSessions)
	require.Equal(t, 30, ev.SessionMinutes)

	minutes999 := 999
	_, err = svc.CreateEvent(event.CreateEventInput{
		Name:           "Invalid Minutes Event",
		Venue:          "GBK",
		SaleStartAt:    time.Now().Add(time.Hour),
		SessionMinutes: &minutes999,
	})
	require.ErrorIs(t, err, event.ErrValidation, "session_minutes di luar 5..120 harus ditolak")
}

func TestInvalidPathIDReturnsSingle400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// service nil membuktikan handler berhenti sebelum menyentuh service saat ID tidak valid.
	h := event.NewHandler(nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/catalog/abc", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "response harus berupa satu objek JSON utuh")
	require.NotEmpty(t, body["error"])
}

func TestCreateEventPosterURLValidation(t *testing.T) {
	db := testutil.DB(t)
	svc := event.NewService(event.NewRepository(db))
	future := time.Now().Add(time.Hour)

	valid := "  https://cdn.example.com/posters/bmth.jpg  "
	ev1, err := svc.CreateEvent(event.CreateEventInput{
		Name: "BMTH", Venue: "GBK", PosterURL: &valid, SaleStartAt: future,
	})
	require.NoError(t, err)
	require.NotNil(t, ev1.PosterURL)
	require.Equal(t, "https://cdn.example.com/posters/bmth.jpg", *ev1.PosterURL)

	blank := "   "
	ev2, err := svc.CreateEvent(event.CreateEventInput{
		Name: "Blank Poster", Venue: "GBK", PosterURL: &blank, SaleStartAt: future,
	})
	require.NoError(t, err)
	require.Nil(t, ev2.PosterURL)

	xss := "javascript:alert(1)"
	_, err = svc.CreateEvent(event.CreateEventInput{
		Name: "Unsafe Poster", Venue: "GBK", PosterURL: &xss, SaleStartAt: future,
	})
	require.ErrorIs(t, err, event.ErrValidation, "skema non-http/https wajib ditolak")
}

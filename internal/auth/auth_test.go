package auth_test

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ticent/internal/auth"
	"ticent/internal/platform/testutil"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterConcurrentDuplicateAndCaseInsensitive(t *testing.T) {
	db := testutil.DB(t)
	svc := auth.NewService(auth.NewRepository(db), "test-secret")

	const attempts = 10
	var created, taken, other atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := range attempts {
		email := "fan@bmth.com"
		if i%2 == 0 {
			email = "  Fan@BMTH.com  "
		}
		wg.Go(func() {
			<-start
			_, err := svc.Register(email, fmt.Sprintf("Fan %d", i), "password123")
			switch {
			case err == nil:
				created.Add(1)
			case errors.Is(err, auth.ErrEmailTaken):
				taken.Add(1)
			default:
				other.Add(1)
				t.Log("unexpected register error:", err)
			}
		})
	}
	close(start)
	wg.Wait()

	require.EqualValues(t, 1, created.Load(), "hanya 1 akun boleh terbuat")
	require.EqualValues(t, attempts-1, taken.Load(), "sisanya harus ErrEmailTaken (409), bukan 500")
	require.Zero(t, other.Load(), "tidak boleh ada error tak terduga")

	var storedEmail string
	require.NoError(t, db.Raw(`SELECT email FROM users LIMIT 1`).Scan(&storedEmail).Error)
	require.Equal(t, "fan@bmth.com", storedEmail)

	token, err := svc.Login("  FAN@BMTH.COM  ", "password123")
	require.NoError(t, err, "login tidak boleh gagal hanya karena beda huruf besar/spasi")
	require.NotEmpty(t, token)
}

func TestRegisterValidationEdgeCases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	svc := auth.NewService(auth.NewRepository(db), "test-secret")

	// 1) Nama hanya berisi spasi wajib ditolak 400, bukan disimpan kosong.
	_, err := svc.Register("blank@bmth.com", "   ", "password123")
	require.Error(t, err)

	// 2) Password > 72 byte (batas bcrypt) wajib ditolak sebagai error validasi (400), bukan error tak terduga (500).
	r := gin.New()
	auth.NewHandler(svc).RegisterRoutes(r, func(c *gin.Context) { c.Next() })

	longPass := strings.Repeat("a", 73)
	recLong := httptest.NewRecorder()
	reqLong := httptest.NewRequest(http.MethodPost, "/auth/register",
		bytes.NewBufferString(`{"email":"long@bmth.com","full_name":"Fan","password":"`+longPass+`"}`))
	reqLong.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(recLong, reqLong)
	require.Equal(t, http.StatusBadRequest, recLong.Code)

	// 3) Spasi di sekitar email tetap diterima lewat jalur HTTP.
	recTrim := httptest.NewRecorder()
	reqTrim := httptest.NewRequest(http.MethodPost, "/auth/register",
		bytes.NewBufferString(`{"email":"  Spaced@BMTH.com  ","full_name":"Fan","password":"password123"}`))
	reqTrim.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(recTrim, reqTrim)
	require.Equal(t, http.StatusCreated, recTrim.Code)
}

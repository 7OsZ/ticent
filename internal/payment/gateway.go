package payment

import (
	"net/http"
	"time"

	"github.com/midtrans/midtrans-go"
	"github.com/midtrans/midtrans-go/coreapi"
	"github.com/midtrans/midtrans-go/snap"
)

var wib = time.FixedZone("WIB", 7*60*60)

const midtransTimeLayout = "2006-01-02 15:04:05 -0700"

// SnapRequest = data ringkas yang dikirim ke Midtrans Snap untuk membuat halaman pembayaran.
type SnapRequest struct {
	OrderID      string
	GrossAmount  int64
	CustomerName string
	CustomerMail string
	StartTime    time.Time
	DurationMin  int
}

// StatusResult = status transaksi hasil tanya ke Status API Midtrans (dipakai POST /orders/:id/sync).
type StatusResult struct {
	OrderID           string
	StatusCode        string
	GrossAmount       string
	TransactionStatus string
	FraudStatus       string
}

// Gateway membungkus komunikasi ke Midtrans supaya dapat diganti tiruan (fake) di test otomatis.
type Gateway interface {
	CreateTransaction(req SnapRequest) (token, redirectURL string, err error)
	CheckTransaction(midtransOrderID string) (*StatusResult, error)
}

// MidtransGateway berbicara langsung ke Midtrans Snap & Core API memakai library resmi midtrans-go.
type MidtransGateway struct {
	snap snap.Client
	core coreapi.Client
}

func NewMidtransGateway(serverKey string, isProduction bool) *MidtransGateway {
	env := midtrans.Sandbox
	if isProduction {
		env = midtrans.Production
	}
	// Matikan logger bawaan midtrans-go supaya header Authorization Basic (ServerKey) tidak pernah
	// tercetak ke stdout pada mode Sandbox, dan batasi timeout HTTP ke 15 detik.
	silentHTTP := &midtrans.HttpClientImplementation{
		HttpClient: &http.Client{Timeout: 15 * time.Second},
		Logger:     &midtrans.LoggerImplementation{LogLevel: midtrans.NoLogging},
	}

	var s snap.Client
	s.New(serverKey, env)
	s.HttpClient = silentHTTP

	var c coreapi.Client
	c.New(serverKey, env)
	c.HttpClient = silentHTTP

	return &MidtransGateway{snap: s, core: c}
}

func (g *MidtransGateway) CreateTransaction(req SnapRequest) (string, string, error) {
	resp, mErr := g.snap.CreateTransaction(&snap.Request{
		TransactionDetails: midtrans.TransactionDetails{
			OrderID:  req.OrderID,
			GrossAmt: req.GrossAmount,
		},
		CustomerDetail: &midtrans.CustomerDetails{
			FName: req.CustomerName,
			Email: req.CustomerMail,
		},
		// Batas bayar Midtrans dibuat lebih pendek dari hold 30 menit (A-04) supaya tidak bayar di detik hangus.
		Expiry: &snap.ExpiryDetails{
			StartTime: req.StartTime.In(wib).Format(midtransTimeLayout),
			Unit:      "minute",
			Duration:  int64(req.DurationMin),
		},
	})
	if mErr != nil {
		return "", "", mErr
	}
	return resp.Token, resp.RedirectURL, nil
}

func (g *MidtransGateway) CheckTransaction(midtransOrderID string) (*StatusResult, error) {
	resp, mErr := g.core.CheckTransaction(midtransOrderID)
	if mErr != nil {
		// Sebelum user memilih metode bayar di Snap, Midtrans belum mengenal transaksi ini (404).
		if mErr.StatusCode == http.StatusNotFound {
			return &StatusResult{OrderID: midtransOrderID, StatusCode: "404", TransactionStatus: "pending"}, nil
		}
		return nil, mErr
	}
	if resp.StatusCode == "404" {
		return &StatusResult{OrderID: midtransOrderID, StatusCode: "404", TransactionStatus: "pending"}, nil
	}
	return &StatusResult{
		OrderID:           resp.OrderID,
		StatusCode:        resp.StatusCode,
		GrossAmount:       resp.GrossAmount,
		TransactionStatus: resp.TransactionStatus,
		FraudStatus:       resp.FraudStatus,
	}, nil
}

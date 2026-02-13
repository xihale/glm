package interfaces

import "time"

type QuotaStatus struct {
	Used      int64     `json:"used"`
	Limit     int64     `json:"limit"`
	Remaining int64     `json:"remaining"`
	ResetTime time.Time `json:"reset_time"`
	Type      string    `json:"type"`
	Raw       string    `json:"-"`
}

type Provider interface {
	Name() string
	Authenticate() error
	GetQuota() (*QuotaStatus, error)
	SendHeartbeat() error
	SetDebug(bool)
}

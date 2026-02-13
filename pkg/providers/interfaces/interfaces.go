package interfaces

import (
	"time"
)

type QuotaStatus struct {
	Used        int64
	Limit       int64
	Remaining   int64
	ResetTime   time.Time
	Type        string
	Raw         string
	CliQuotaRaw string // Deprecated: Moved to separate provider
}

type Provider interface {
	Name() string
	ID() string
	Authenticate() error
	GetQuota() (*QuotaStatus, error)
	Activate(w interface{}, debug bool, force bool) error
	SetDebug(debug bool)
}

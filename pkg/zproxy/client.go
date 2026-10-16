// Package zproxy talks to the local zproxy gateway's reset-card endpoints
// (zcode-plan-proxy, port 8870): engine status, manual use/claim, and the
// auto/manual policy switch. The engine itself lives in zproxy — this is a
// remote control, not a second brain.
package zproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is zproxy's local gateway.
const DefaultBaseURL = "http://127.0.0.1:8870"

// Valid windows for reset cards.
const (
	WindowFiveHour = "five_hour"
	WindowWeek     = "week"
)

// Client calls zproxy's local /reset/* endpoints.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient builds a client; empty baseURL means the default.
func NewClient(baseURL string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Policy mirrors zproxy's reset policy (thresholds in percent/minutes).
type Policy struct {
	Mode            string          `json:"mode"`
	Triggers        map[string]bool `json:"triggers"`
	ExhaustedPct    float64         `json:"exhaustedPct"`
	ResetGapMin     float64         `json:"resetGapMin"`
	ExpiryFireMin   float64         `json:"expiryFireMin"`
	ValueMaxPctPerH float64         `json:"valueMaxPctPerH"`
	MinGainPct      float64         `json:"minGainPct"`
	CooldownMin     float64         `json:"cooldownMin"`
}

// Window is one quota window reading from zproxy's monitor. Times are
// epoch milliseconds, as zproxy serves them.
type Window struct {
	Kind          string   `json:"kind"`
	Percentage    *float64 `json:"percentage"`
	NextResetTime *int64   `json:"nextResetTime"`
}

// Decision is zproxy's verdict for one window: whether it would use a card
// now, and why (or why not).
type Decision struct {
	Use       bool    `json:"use"`
	Reason    string  `json:"reason"`
	Window    *Window `json:"window"`
	Cards     []int64 `json:"cards"`
	Supported bool    `json:"supported"`
}

// Auth says whether the reset endpoints have their JWTs.
type Auth struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// Status is the engine snapshot: mode, decisions per window, claim schedule,
// last use per window.
type Status struct {
	Mode   string  `json:"mode"`
	Policy *Policy `json:"policy"`
	Auth   Auth    `json:"auth"`
	Claim  struct {
		NextClaimAt int64   `json:"nextClaimAt"`
		Key         *string `json:"key"`
	} `json:"claim"`
	LastUse map[string]int64    `json:"lastUse"`
	Windows map[string]Decision `json:"windows"`
}

func (c *Client) do(method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zproxy unreachable (%v); is it running with --reset-auto? see: systemctl --user status zproxy", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			return nil, fmt.Errorf("zproxy %d: %s", res.StatusCode, e.Error.Message)
		}
		return nil, fmt.Errorf("zproxy %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// Status fetches the engine snapshot.
func (c *Client) Status() (*Status, error) {
	data, err := c.do(http.MethodGet, "/reset/status", nil)
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("zproxy status unreadable: %w", err)
	}
	return &s, nil
}

// Use spends one card on the window now (server picks the earliest-expiring
// card). Returns whether the server confirmed the use.
func (c *Client) Use(window string) (bool, error) {
	if window != WindowFiveHour && window != WindowWeek {
		return false, fmt.Errorf("unknown window %q (use five_hour or week)", window)
	}
	data, err := c.do(http.MethodPost, "/reset/use", map[string]string{"resetType": window})
	if err != nil {
		return false, err
	}
	var r struct {
		Used bool `json:"used"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return false, fmt.Errorf("zproxy use unreadable: %w", err)
	}
	return r.Used, nil
}

// Claim asks the server for a new card (grants are throttled server-side;
// zproxy schedules the retry).
func (c *Client) Claim() error {
	_, err := c.do(http.MethodPost, "/reset/claim", nil)
	return err
}

// SetMode flips the engine policy between auto and manual.
func (c *Client) SetMode(mode string) (*Policy, error) {
	if mode != "auto" && mode != "manual" {
		return nil, fmt.Errorf("mode must be auto or manual, got %q", mode)
	}
	data, err := c.do(http.MethodPut, "/reset/policy", map[string]string{"mode": mode})
	if err != nil {
		return nil, err
	}
	var r struct {
		Policy Policy `json:"policy"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("zproxy policy unreadable: %w", err)
	}
	return &r.Policy, nil
}

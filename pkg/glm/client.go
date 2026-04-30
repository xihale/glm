package glm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/httputil"
	"github.com/xihale/glm/pkg/log"
)

const (
	DefaultBaseURL    = "https://open.bigmodel.cn"
	QuotaEndpoint     = "/api/monitor/usage/quota/limit"
	HeartbeatEndpoint = "/api/coding/paas/v4/chat/completions"
	UserAgent         = "OpenClaw/2026.3.19"
	HeartbeatModel    = "glm-4.7"
	HeartbeatPrompt   = "Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. Do not infer or repeat old tasks from prior chats. If nothing needs attention, reply HEARTBEAT_OK."

	// Activate verification
	VerifyRetries  = 5
	VerifyInterval = 3 * time.Second
	ResetThreshold = 10 * time.Minute
)

type QuotaStatus struct {
	Used      int64
	Limit     int64
	Remaining int64
	ResetTime time.Time
	Raw       string
}

type Client struct {
	APIKey  string
	BaseURL string
	client  *http.Client
	Debug   bool
}

func NewClient() *Client {
	baseURL := DefaultBaseURL
	if config.Current.BaseURL != "" {
		baseURL = config.Current.BaseURL
	}

	return &Client{
		APIKey:  config.Current.APIKey,
		BaseURL: baseURL,
		client:  httputil.NewHttpClient(10 * time.Second),
	}
}

func (c *Client) SetDebug(d bool) { c.Debug = d }

func (c *Client) GetQuota() (*QuotaStatus, error) {
	req, err := http.NewRequest("GET", c.BaseURL+QuotaEndpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quota failed: %d", resp.StatusCode)
	}

	var qResp quotaResponse
	if err := json.Unmarshal(body, &qResp); err != nil {
		return &QuotaStatus{Raw: string(body)}, nil
	}

	return parseQuotaResponse(body, qResp), nil
}

func (c *Client) SendHeartbeat() error {
	payload := map[string]interface{}{
		"type":      "heartbeat",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"model":     HeartbeatModel,
		"messages": []map[string]string{
			{"role": "user", "content": HeartbeatPrompt},
		},
		"max_tokens": 5,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", c.BaseURL+HeartbeatEndpoint, bytes.NewBuffer(body))
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	log.Debugf("Sending heartbeat to %s", c.BaseURL+HeartbeatEndpoint)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Debugf("Heartbeat failed with status %d: %s", resp.StatusCode, string(respBody))
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// Activate sends heartbeat and verifies quota was refreshed.
// If reset is imminent (< ResetThreshold), sleeps until reset then retries.
// serviceMode: when true, auto-sleeps and retries on imminent reset.
func (c *Client) Activate(force bool, serviceMode bool) (*QuotaStatus, error) {
	// Step 1: check current quota
	quota, err := c.GetQuota()
	if err != nil {
		return nil, fmt.Errorf("get quota: %w", err)
	}

	// Already active and not forcing
	if !force && quota.Remaining < 100 {
		return quota, nil
	}

	// Step 2: if reset is imminent, sleep until reset
	if serviceMode && !quota.ResetTime.IsZero() {
		until := time.Until(quota.ResetTime)
		if until > 0 && until < ResetThreshold {
			log.Infof("Reset in %v, sleeping until %s", until, quota.ResetTime.Format("15:04:05"))
			time.Sleep(until + 2*time.Second)
			// Re-check quota after sleep
			quota, err = c.GetQuota()
			if err != nil {
				return nil, fmt.Errorf("get quota after sleep: %w", err)
			}
		}
	}

	// Step 3: send heartbeat
	if err := c.SendHeartbeat(); err != nil {
		return quota, err
	}

	// Step 4: verify activation
	verified := false
	for i := 0; i < VerifyRetries; i++ {
		time.Sleep(VerifyInterval)
		q, err := c.GetQuota()
		if err != nil {
			log.Debugf("Verify attempt %d: quota error: %v", i+1, err)
			continue
		}
		if q.Remaining < 100 {
			quota = q
			verified = true
			break
		}
		log.Debugf("Verify attempt %d: still 100%% remaining, retrying...", i+1)
	}

	if !verified {
		log.Debugf("Heartbeat sent but verification inconclusive after %d attempts", VerifyRetries)
	}

	// Step 5: check if new reset is imminent (service mode auto-retry)
	if serviceMode && !quota.ResetTime.IsZero() {
		until := time.Until(quota.ResetTime)
		if until > 0 && until < ResetThreshold {
			log.Infof("New cycle reset in %v, sleeping and retrying", until)
			time.Sleep(until + 2*time.Second)
			return c.Activate(false, serviceMode)
		}
	}

	return quota, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Accept", "application/json")
}

// --- internal types ---

type quotaResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Limits []struct {
			Type          string  `json:"type"`
			Remaining     int64   `json:"remaining"`
			Usage         int64   `json:"usage"`
			Percentage    float64 `json:"percentage"`
			NextResetTime int64   `json:"nextResetTime"`
		} `json:"limits"`
	} `json:"data"`
}

func parseQuotaResponse(raw []byte, qResp quotaResponse) *QuotaStatus {
	var used int64
	var remaining int64 = 100
	var resetTime time.Time
	var found bool

	for _, l := range qResp.Data.Limits {
		if l.Type == "TOKENS_LIMIT" {
			used = int64(l.Percentage)
			remaining = 100 - used
			if l.NextResetTime > 0 {
				resetTime = time.Unix(l.NextResetTime/1000, (l.NextResetTime%1000)*1000000)
			}
			found = true
			break
		}
	}

	if !found {
		for _, l := range qResp.Data.Limits {
			if l.Type == "TIME_LIMIT" {
				remaining = l.Remaining
				used = 100 - remaining
				if l.NextResetTime > 0 {
					resetTime = time.Unix(l.NextResetTime/1000, (l.NextResetTime%1000)*1000000)
				}
				break
			}
		}
	}

	return &QuotaStatus{
		Used:      used,
		Limit:     100,
		Remaining: remaining,
		ResetTime: resetTime,
		Raw:       string(raw),
	}
}

// FormatTimeUntil returns human-readable duration until t.
func FormatTimeUntil(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "Passed"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return "0m"
}

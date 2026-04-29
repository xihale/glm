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
	"github.com/xihale/glm/pkg/providers/interfaces"
	pkgutils "github.com/xihale/glm/pkg/utils"
)

const (
	BaseURL = "https://open.bigmodel.cn"

	QuotaEndpoint     = "/api/monitor/usage/quota/limit"
	HeartbeatEndpoint = "/api/coding/paas/v4/chat/completions"
	UserAgent         = "ClaudeCode/2.1.27"
	ClientEnv         = "claude-code"
	HeartbeatModel    = "glm-4.7"
)

type Provider struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
	Debug   bool
	Config  config.ProviderConfig
	total   int
}

func NewProvider() *Provider {
	return &Provider{
		BaseURL: BaseURL,
		Client:  httputil.NewHttpClient(10 * time.Second),
	}
}

func NewProviderWithConfig(cfg config.ProviderConfig) *Provider {
	p := NewProvider()
	p.Config = cfg
	if cfg.BaseURL != "" {
		p.BaseURL = cfg.BaseURL
	}
	return p
}

// SetTotal sets the total number of providers for display purposes.
func (p *Provider) SetTotal(n int) { p.total = n }

func (p *Provider) Name() string {
	if p.total <= 1 {
		return ""
	}
	if p.Config.Name != "" {
		return "[GLM - " + p.Config.Name + "]"
	}
	return "[GLM]"
}
func (p *Provider) ID() string {
	if p.Config.Name != "" {
		return "glm_" + p.Config.Name
	}
	return "glm"
}
func (p *Provider) SetDebug(d bool) { p.Debug = d }

func (p *Provider) Authenticate() error {
	if p.Config.APIKey != "" {
		p.APIKey = p.Config.APIKey
	} else {
		p.APIKey = config.Current.GLM.APIKey
	}

	if p.APIKey == "" {
		return fmt.Errorf("GLM API Key not found in config")
	}
	if config.Current.GLM.BaseURL != "" && p.BaseURL == BaseURL {
		p.BaseURL = config.Current.GLM.BaseURL
	}
	return nil
}

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

func (p *Provider) GetQuota() (*interfaces.QuotaStatus, error) {
	req, err := http.NewRequest("GET", p.BaseURL+QuotaEndpoint, nil)
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)

	resp, err := p.Client.Do(req)
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
		return &interfaces.QuotaStatus{Type: "glm_json_error", Raw: string(body)}, nil
	}

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

	return &interfaces.QuotaStatus{
		Used:      used,
		Limit:     100,
		Remaining: remaining,
		ResetTime: resetTime,
		Type:      "request_percentage",
		Raw:       string(body),
	}, nil
}

// Activate sends a heartbeat to commit a new quota cycle.
//
// The server behavior is:
//   - When a cycle resets, the server shows Remaining=100% with a ResetTime in the
//     future, but the new cycle does NOT truly start until the first request (commit)
//     is sent. Until then the server keeps pushing the start/reset time forward.
//   - After activation, the server returns Remaining=100% with a new ResetTime ~5h
//     in the future (same data, but the cycle is now truly active).
//   - When Remaining < 100%, the cycle is in use — sending heartbeat is useless.
//
// Since uncommitted and committed states return identical data (Remaining=100% +
// future ResetTime), Activate cannot distinguish them from quota alone. The daemon
// handles this by scheduling itself right after the expected reset time.
//
// In non-force mode: skip if Remaining < 100% (in use, heartbeat is useless).
// Otherwise (Remaining == 100%): send heartbeat to ensure the cycle is committed.
// In force mode: always sends heartbeat.
func (p *Provider) Activate(w io.Writer, debug bool, force bool) (*interfaces.QuotaStatus, error) {
	quota, err := p.GetQuota()
	if err != nil {
		return nil, err
	}

	if !force {
		// Remaining < 100% means the cycle is in use — heartbeat is useless.
		if quota.Remaining < 100 {
			timeStr := pkgutils.FormatTimeUntil(quota.ResetTime)
			resetAt := quota.ResetTime.Local().Format("15:04:05")
			fmt.Printf("%s in use (%d%%, %s, reset at %s)\n", p.Name(), quota.Remaining, timeStr, resetAt)
			return quota, nil
		}
	}

	err = p.SendHeartbeat()
	if err != nil {
		pkgutils.FormatActivationError(err, debug)
		return quota, err
	}

	fmt.Printf("%s \033[32mactivated\033[0m\n", p.Name())
	return quota, nil
}

func (p *Provider) SendHeartbeat() error {
	payload := map[string]interface{}{
		"model":      HeartbeatModel,
		"messages":   []map[string]string{{"role": "user", "content": "commit"}},
		"max_tokens": 5,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", p.BaseURL+HeartbeatEndpoint, bytes.NewBuffer(body))
	p.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// setHeaders applies common headers to all requests.
// Mirrors headers used by CLI coding agents like ClaudeCode/Forge:
//   - Authorization: Bearer <api_key> (standard OpenAI-compatible auth)
//   - User-Agent: identifies the client
//   - X-Client-Environment: identifies the client environment
//   - Connection: keep-alive for connection reuse
//   - Accept: application/json
func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("X-Client-Environment", ClientEnv)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Accept", "application/json")
}

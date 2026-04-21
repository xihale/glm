package glm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"glm/pkg/config"
	"glm/pkg/httputil"
	"glm/pkg/providers/interfaces"
	pkgutils "glm/pkg/utils"
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

func (p *Provider) Activate(w interface{}, debug bool, force bool) (*interfaces.QuotaStatus, error) {
	quota, err := p.GetQuota()
	if err == nil && !force {
		timeStr := pkgutils.FormatTimeUntil(quota.ResetTime)
		// When time until reset is "0m" or already passed, the reset
		// hasn't taken effect yet. Sending a heartbeat now is wasteful
		// — skip and tell the user to wait for the reset boundary.
		if timeStr == "0m" || timeStr == "Passed" {
			at := quota.ResetTime.Add(5 * time.Second).Local().Format("15:04:05")
			fmt.Printf("%s \033[33mwaiting for reset\033[0m (reset at %s)\n", p.Name(), at)
			return quota, nil
		}

		at := quota.ResetTime.Add(5 * time.Second).Local().Format("15:04:05")
		fmt.Printf("%s skipped (%d%%, %s, reset at %s)\n", p.Name(), quota.Remaining, timeStr, at)
		return quota, nil
	}

	err = p.SendHeartbeat()
	if err != nil {
		pkgutils.FormatActivationError(err, debug)
	} else {
		fmt.Printf("%s \033[32mactivated\033[0m\n", p.Name())
	}
	return quota, nil
}

func (p *Provider) SendHeartbeat() error {
	payload := map[string]interface{}{
		"model":      HeartbeatModel,
		"messages":   []map[string]string{{"role": "user", "content": "hello"}},
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

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", p.APIKey)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("X-Client-Environment", ClientEnv)
}

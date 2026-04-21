package glm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"ai-daemon/pkg/config"
	"ai-daemon/pkg/httputil"
	"ai-daemon/pkg/providers/interfaces"
	pkgutils "ai-daemon/pkg/utils"
)

const (
	BaseURL = "https://open.bigmodel.cn"

	QuotaEndpoint     = "/api/monitor/usage/quota/limit"
	HeartbeatEndpoint = "/api/coding/paas/v4/chat/completions"
	UserAgent         = "ClaudeCode/2.1.27"
	ClientEnv         = "opencode-cli"
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

func (p *Provider) Activate(w interface{}, debug bool, force bool) error {
	var writer io.Writer
	if w != nil {
		if wr, ok := w.(io.Writer); ok {
			writer = wr
		}
	}
	if writer == nil {
		writer = os.Stdout
	}

	quota, err := p.GetQuota()
	if err == nil {
		timeStr := pkgutils.FormatTimeUntil(quota.ResetTime)
		// For GLM, we only skip if it's not "0m" and not "Passed"
		// This ensures that when it shows "0m", activation is always allowed.
		if !force && timeStr != "0m" && timeStr != "Passed" {
			q := pkgutils.ModelQuota{Remaining: float64(quota.Remaining), ResetTime: quota.ResetTime}
			pkgutils.PrintSkipMessageWithWriter(writer, "General", q)
			return nil
		}
	}

	fmt.Fprintf(writer, "  [*] Activating %-25s ... ", "General")
	err = p.SendHeartbeat()

	if err != nil {
		pkgutils.FormatActivationErrorWithWriter(writer, err, debug)
	} else {
		fmt.Fprintf(writer, "\033[32m[+] Success\033[0m\n")
	}
	return nil
}

func (p *Provider) SendHeartbeat() error {
	payload := map[string]interface{}{
		"model":      "glm-4.7",
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
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

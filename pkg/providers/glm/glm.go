package glm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/interfaces"
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
}

func NewProvider() *Provider {
	return &Provider{
		BaseURL: BaseURL,
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *Provider) Name() string { return "GLM Coding Plan" }
func (p *Provider) ID() string   { return "glm" }
func (p *Provider) SetDebug(d bool) { p.Debug = d }

func (p *Provider) Authenticate() error {
	p.APIKey = config.Current.GLM.APIKey
	if p.APIKey == "" {
		return fmt.Errorf("GLM API Key not found in config")
	}
	if config.Current.GLM.BaseURL != "" {
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
	if err != nil { return nil, err }
	p.setHeaders(req)

	resp, err := p.Client.Do(req)
	if err != nil { return nil, err }
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

func (p *Provider) Activate(debug bool, force bool) error {
	fmt.Printf("  [*] Sending Heartbeat ... ")
	err := p.SendHeartbeat()
	if err != nil {
		fmt.Printf("\033[31m[-] %v\033[0m\n", err)
	} else {
		fmt.Printf("\033[32m[+] Success\033[0m\n")
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
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return fmt.Errorf("status %d", resp.StatusCode) }
	return nil
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", p.APIKey)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("X-Client-Environment", ClientEnv)
}

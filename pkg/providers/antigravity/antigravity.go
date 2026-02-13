package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-daemon/internal/utils"
	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/interfaces"
)

type Provider struct {
	Client *http.Client
	Debug  bool
}

func NewProvider() *Provider {
	return &Provider{
		Client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *Provider) Name() string { return "Antigravity IDE" }
func (p *Provider) ID() string   { return "antigravity" }
func (p *Provider) SetDebug(d bool) { p.Debug = d }

func (p *Provider) Authenticate() error {
	if config.Current.Gemini.AccessToken == "" {
		return fmt.Errorf("no access token available")
	}
	return nil
}

func (p *Provider) GetQuota() (*interfaces.QuotaStatus, error) {
	token := config.Current.Gemini.AccessToken
	projectID := config.Current.Gemini.ProjectID

	if projectID == "" {
		var err error
		projectID, err = utils.FetchProjectID(token)
		if err != nil {
			return nil, err
		}
	}

	url := "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	payload := fmt.Sprintf(`{"project": "%s"}`, projectID)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Antigravity/1.15.8 Chrome/138.0.7204.235 Electron/37.3.1 Safari/537.36")

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch failed: %d", resp.StatusCode)
	}

	// Simplistic extraction for interface compatibility
	return &interfaces.QuotaStatus{
		Type: "antigravity_remote",
		Raw:  string(body),
	}, nil
}

func (p *Provider) Activate(debug bool, force bool) error {
	token := config.Current.Gemini.AccessToken
	projectID := config.Current.Gemini.ProjectID

	models := []string{"gemini-3-flash", "gemini-3-pro-low", "claude-sonnet-4-5"}
	
	for _, m := range models {
		fmt.Printf("  [*] Warmup %-18s ... ", m)
		err := p.sendWarmup(token, projectID, m)
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				fmt.Printf("\033[33m[!] Busy\033[0m\n")
			} else {
				fmt.Printf("\033[31m[-] %v\033[0m\n", err)
			}
		} else {
			fmt.Printf("\033[32m[+] Success\033[0m\n")
		}
		time.Sleep(1 * time.Second)
	}
	return nil
}

func (p *Provider) sendWarmup(token, projectID, model string) error {
	url := "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
	
	// Create a dynamic fingerprint for this request
	// (Note: In a real implementation, GenerateFingerprint would be used here)
	
	body := map[string]interface{}{
		"project": projectID,
		"model":   model,
		"request": map[string]interface{}{
			"contents": []map[string]interface{}{{"role": "user", "parts": []map[string]string{{"text": "hi"}}}},
			"generationConfig": map[string]interface{}{
				"maxOutputTokens": 4096,
				"thinkingConfig":  map[string]interface{}{"include_thoughts": true, "thinking_budget": 4000},
			},
		},
		"requestType": "agent",
		"userAgent":   "antigravity",
	}
	
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/1.15.8 windows/amd64")
	
	resp, err := p.Client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { return fmt.Errorf("status %d", resp.StatusCode) }
	return nil
}

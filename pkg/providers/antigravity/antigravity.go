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
	pkgutils "ai-daemon/pkg/utils"
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
		if err != nil { return nil, err }
	}

	url := "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	payload := fmt.Sprintf(`{"project": "%s"}`, projectID)

	req, _ := http.NewRequest("POST", url, strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/1.15.8 windows/amd64")

	resp, err := p.Client.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return &interfaces.QuotaStatus{Type: "antigravity_remote", Raw: string(body)}, nil
}

func (p *Provider) Activate(debug bool, force bool) error {
	token := config.Current.Gemini.AccessToken
	projectID := config.Current.Gemini.ProjectID

	quota, err := p.GetQuota()
	if err != nil { return err }

	modelMap := pkgutils.ExtractAllModelQuotas(quota.Raw)
	models := []struct{ID, Label string}{
		{"gemini-3-flash", "Gemini 3 Flash"},
		{"gemini-3-pro-low", "Gemini 3 Pro"},
		{"claude-sonnet-4-5", "Claude Sonnet 4.5"},
	}
	
	for _, m := range models {
		info, ok := modelMap[m.ID]
		timeUntil := time.Until(info.ResetTime)
		
		// Skip only if 0 < timeUntil < 4h 59m
		if !force && ok && !info.ResetTime.IsZero() && timeUntil > 0 && timeUntil < (4*time.Hour+59*time.Minute) {
			fmt.Printf("  [*] Activating %-18s ... \033[33mSkipped\033[0m (%s left)\n", m.Label, pkgutils.FormatTimeUntil(info.ResetTime))
			continue
		}

		fmt.Printf("  [*] Activating %-18s ... ", m.Label)
		err := p.sendActivation(token, projectID, m.ID)
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				fmt.Printf("\033[31m[-] Busy (429)\033[0m\n")
			} else {
				fmt.Printf("\033[31m[-] Error: %v\033[0m\n", err)
			}
		} else {
			fmt.Printf("\033[32m[+] Success\033[0m\n")
		}
		time.Sleep(5 * time.Second)
	}
	return nil
}

func (p *Provider) sendActivation(token, projectID, model string) error {
	url := "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
	deviceId, quotaUser := pkgutils.GenerateFingerprint(config.Current.Gemini.AccessToken)
	
	body := map[string]interface{}{
		"project": projectID, "model": model,
		"request": map[string]interface{}{
			"contents": []map[string]interface{}{{"role": "user", "parts": []map[string]string{{"text": "hi"}}}},
			"generationConfig": map[string]interface{}{
				"maxOutputTokens": 4096,
				"thinkingConfig":  map[string]interface{}{"include_thoughts": true, "thinking_budget": 4000},
			},
		},
		"requestType": "agent", "userAgent": "antigravity",
	}
	
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/1.15.8 windows/amd64")
	req.Header.Set("X-Goog-Api-Client", pkgutils.GetRandomXGoogClient())
	req.Header.Set("Client-Metadata", fmt.Sprintf(`{"ideType":"ANTIGRAVITY","platform":"WINDOWS","pluginType":"GEMINI","deviceId":"%s","quotaUser":"%s"}`, deviceId, quotaUser))
	
	resp, err := p.Client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { return fmt.Errorf("status %d", resp.StatusCode) }
	return nil
}

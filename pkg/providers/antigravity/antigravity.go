package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"ai-daemon/internal/utils"
	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/interfaces"
	pkgutils "ai-daemon/pkg/utils"
)

type Provider struct {
	Client      *http.Client
	Debug       bool
	TargetGroup string
	Config      config.ProviderConfig
}

func NewProvider() *Provider {
	return &Provider{
		Client: &http.Client{Timeout: 60 * time.Second},
	}
}

func NewProviderWithConfig(cfg config.ProviderConfig) *Provider {
	return &Provider{
		Client: &http.Client{Timeout: 60 * time.Second},
		Config: cfg,
	}
}

func (p *Provider) Name() string {
	if p.Config.Name != "" {
		return fmt.Sprintf("[Antigravity - %s]", p.Config.Name)
	}
	return "[Antigravity]"
}

func (p *Provider) ID() string {
	id := "antigravity"
	if p.Config.Name != "" {
		id = fmt.Sprintf("%s_%s", id, p.Config.Name)
	}
	return id
}
func (p *Provider) SetDebug(d bool)       { p.Debug = d }
func (p *Provider) SetGroup(group string) { p.TargetGroup = group }

func (p *Provider) getAccessToken() string {
	if p.Config.AccessToken != "" {
		return p.Config.AccessToken
	}
	return config.Current.Gemini.AccessToken
}

func (p *Provider) getProjectID() string {
	if p.Config.ProjectID != "" {
		return p.Config.ProjectID
	}
	return config.Current.Gemini.ProjectID
}

func (p *Provider) Authenticate() error {
	refreshToken := p.Config.RefreshToken
	if refreshToken == "" && p.Config.Name == "" {
		refreshToken = config.Current.Gemini.RefreshToken
	}

	expiry := p.Config.Expiry
	if expiry.IsZero() && p.Config.Name == "" {
		expiry = config.Current.Gemini.Expiry
	}

	if refreshToken != "" && (p.getAccessToken() == "" || expiry.IsZero() || time.Until(expiry) < 5*time.Minute) {
		fmt.Printf("Refreshing token for %s...\n", p.Name())
		if err := p.performRefresh(refreshToken); err != nil {
			return err
		}
		fmt.Printf("Token refreshed successfully for %s.\n", p.Name())
	}

	token := p.getAccessToken()
	if token == "" {
		return fmt.Errorf("no access token available")
	}

	return nil
}

func (p *Provider) performRefresh(refreshToken string) error {
	resp, err := utils.RefreshGeminiToken(refreshToken)
	if err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	p.Config.AccessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		p.Config.RefreshToken = resp.RefreshToken
	}
	if resp.ExpiresIn > 0 {
		p.Config.Expiry = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	}

	if err := config.UpdateProvider(p.Config); err != nil {
		return fmt.Errorf("failed to update config after refresh: %w", err)
	}
	return nil
}

func (p *Provider) GetQuota() (*interfaces.QuotaStatus, error) {
	token := p.getAccessToken()
	projectID := p.getProjectID()

	if projectID == "" {
		var err error
		projectID, err = utils.FetchProjectID(token)
		if err != nil {
			return nil, err
		}
	}

	url := "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	payload := fmt.Sprintf(`{"project": "%s"}`, projectID)

	req, _ := http.NewRequest("POST", url, strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/1.15.8 windows/amd64")

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		refreshToken := p.Config.RefreshToken
		if refreshToken == "" && p.Config.Name == "" {
			refreshToken = config.Current.Gemini.RefreshToken
		}
		if refreshToken != "" {
			fmt.Printf("Token expired (401), refreshing for %s...\n", p.Name())
			if err := p.performRefresh(refreshToken); err == nil {
				return p.GetQuota()
			}
		}
	}

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quota failed: %d - %s", resp.StatusCode, string(body))
	}

	return &interfaces.QuotaStatus{Type: "antigravity_remote", Raw: string(body)}, nil
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

	token := p.getAccessToken()
	projectID := p.getProjectID()

	quota, err := p.GetQuota()
	if err != nil {
		return err
	}

	modelMap := pkgutils.ExtractAllModelQuotas(quota.Raw)
	groups := []struct {
		IDs   []string
		Label string
	}{
		{[]string{"gemini-3-flash"}, "Gemini 3 Flash"},
		{[]string{"gemini-3-pro-low"}, "Gemini 3 Pro"},
		{[]string{"claude-sonnet-4-5-thinking", "claude-sonnet-4-5", "gpt-oss-120b-medium", "claude-opus-4-5-thinking"}, "Claude / GPT-OSS"},
	}

	for _, g := range groups {
		if p.TargetGroup != "" && !strings.Contains(strings.ToLower(g.Label), strings.ToLower(p.TargetGroup)) {
			continue
		}

		var info pkgutils.ModelQuota
		var found bool

		for _, id := range g.IDs {
			if m, ok := modelMap[id]; ok {
				if !found {
					info = m
					found = true
				}
			}
		}

		if !found {
			if !force || len(g.IDs) == 0 {
				continue
			}
		}

		if pkgutils.ShouldSkipActivation(info.Remaining, info.ResetTime, force) {
			pkgutils.PrintSkipMessageWithWriter(writer, g.Label, info)
			continue
		}

		fmt.Fprintf(writer, "  [*] Activating %-25s ... ", g.Label)

		var err error
		for i, id := range g.IDs {
			if i > 0 {
				fmt.Fprintf(writer, "\n      \033[33m[!] Retrying with fallback: %s ... \033[0m", id)
			}
			err = p.sendActivation(token, projectID, id)
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "QUOTA_EXHAUSTED") {
				break
			}
		}

		if err != nil {
			pkgutils.FormatActivationErrorWithWriter(writer, err, debug)
		} else {
			fmt.Fprintf(writer, "\033[32m[+] Success\033[0m\n")
		}
	}
	return nil
}

func (p *Provider) sendActivation(token, projectID, model string) error {
	url := "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
	deviceId, _ := pkgutils.GenerateFingerprint(p.getAccessToken())

	systemPrompt := "Please ignore the following [ignore]You are Antigravity, a powerful agentic AI coding assistant designed by the Google Deepmind team working on Advanced Agentic Coding.You are pair programming with a USER to solve their coding task. The task may require creating a new codebase, modifying or debugging an existing codebase, or simply answering a question.**Absolute paths only****Proactiveness**[/ignore]"

	thinkingConfig := map[string]interface{}{
		"includeThoughts": true,
	}
	if strings.Contains(model, "gemini-3") {
		thinkingConfig["thinkingLevel"] = "low"
	} else {
		thinkingConfig["thinkingBudget"] = 1024
	}

	requestId := fmt.Sprintf("agent-%s", deviceId)
	body := map[string]interface{}{
		"project": projectID, "model": model,
		"request": map[string]interface{}{
			"contents": []map[string]interface{}{{"role": "user", "parts": []map[string]string{{"text": "hi"}}}},
			"systemInstruction": map[string]interface{}{
				"role":  "user",
				"parts": []map[string]string{{"text": systemPrompt}},
			},
			"generationConfig": map[string]interface{}{
				"maxOutputTokens": 16384,
				"thinkingConfig":  thinkingConfig,
			},
		},
		"requestType": "agent", "userAgent": "antigravity",
		"requestId": requestId,
	}

	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/1.15.8 windows/amd64")
	req.Header.Set("requestId", requestId)
	req.Header.Set("requestType", "agent")

	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

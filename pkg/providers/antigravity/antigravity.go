package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ai-daemon/internal/utils"
	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/interfaces"

	"github.com/shirou/gopsutil/v3/process"
	"github.com/spf13/viper"
)

type Provider struct {
	Client    *http.Client
	Debug     bool
	LocalPort string
	CSRFToken string
}

func NewProvider() *Provider {
	return &Provider{
		Client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Provider) Name() string {
	return "Antigravity-IDE"
}

func (p *Provider) SetDebug(d bool) {
	p.Debug = d
}

func (p *Provider) Authenticate() error {
	if config.Current.Gemini.AccessToken != "" {
		if p.Debug {
			fmt.Println("DEBUG: Antigravity using Remote OAuth Mode")
		}
		return nil
	}

	procs, err := process.Processes()
	if err != nil {
		return fmt.Errorf("failed to scan processes: %w", err)
	}

	found := false
	for _, proc := range procs {
		name, err := proc.Name()
		if err != nil {
			continue
		}

		if strings.Contains(strings.ToLower(name), "language_server") {
			cmdline, err := proc.Cmdline()
			if err != nil {
				continue
			}

			p.LocalPort = extractArg(cmdline, "--extension_server_port")
			p.CSRFToken = extractArg(cmdline, "--csrf_token")

			if p.LocalPort != "" && p.CSRFToken != "" {
				found = true
				if p.Debug {
					fmt.Printf("DEBUG: Found Antigravity LS (PID %d): Port=%s\n", proc.Pid, p.LocalPort)
				}
				break
			}
		}
	}

	if !found {
		return fmt.Errorf("antigravity language server process not found and no OAuth token configured")
	}

	return nil
}

func extractArg(cmdline, key string) string {
	re := regexp.MustCompile(fmt.Sprintf(`%s\s+([^\s]+)`, key))
	matches := re.FindStringSubmatch(cmdline)
	if len(matches) > 1 {
		return matches[1]
	}

	re2 := regexp.MustCompile(fmt.Sprintf(`%s=([^\s]+)`, key))
	matches2 := re2.FindStringSubmatch(cmdline)
	if len(matches2) > 1 {
		return matches2[1]
	}
	return ""
}

type QuotaResponse struct {
	UserStatus struct {
		PlanStatus struct {
			AvailablePromptCredits int `json:"availablePromptCredits"`
			PlanInfo               struct {
				MonthlyPromptCredits int `json:"monthlyPromptCredits"`
			} `json:"planInfo"`
		} `json:"planStatus"`
		CascadeModelConfigData struct {
			ClientModelConfigs []struct {
				Label        string `json:"label"`
				ModelOrAlias struct {
					Model string `json:"model"`
				} `json:"modelOrAlias"`
				QuotaInfo struct {
					RemainingFraction float64 `json:"remainingFraction"`
					ResetTime         string  `json:"resetTime"`
				} `json:"quotaInfo"`
			} `json:"clientModelConfigs"`
		} `json:"cascadeModelConfigData"`
		UserTier struct {
			Name string `json:"name"`
		} `json:"userTier"`
	} `json:"userStatus"`
}

func (p *Provider) GetQuota() (*interfaces.QuotaStatus, error) {
	if config.Current.Gemini.AccessToken != "" {
		return p.getRemoteQuota()
	}

	url := fmt.Sprintf("http://127.0.0.1:%s/exa.language_server_pb.LanguageServerService/GetUserStatus", p.LocalPort)
	payload := `{"metadata": {"ideName": "antigravity", "extensionName": "antigravity", "ideVersion": "1.15.8", "locale": "en"}}`

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(payload))
	if err != nil {
		return nil, err
	}

	p.setHeaders(req)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(payload)))

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("antigravity status check failed: %s", resp.Status)
	}

	var qResp QuotaResponse
	if err := json.Unmarshal(body, &qResp); err != nil {
		return &interfaces.QuotaStatus{Type: "antigravity_rpc_error", Raw: string(body)}, nil
	}

	var remaining float64
	var resetTime time.Time

	if len(qResp.UserStatus.CascadeModelConfigData.ClientModelConfigs) > 0 {
		cfg := qResp.UserStatus.CascadeModelConfigData.ClientModelConfigs[0]
		for _, c := range qResp.UserStatus.CascadeModelConfigData.ClientModelConfigs {
			if strings.Contains(strings.ToLower(c.Label), "gemini") {
				cfg = c
				break
			}
		}

		remaining = cfg.QuotaInfo.RemainingFraction * 100
		if cfg.QuotaInfo.ResetTime != "" {
			t, err := time.Parse(time.RFC3339, cfg.QuotaInfo.ResetTime)
			if err == nil {
				resetTime = t
			}
		}
	} else if qResp.UserStatus.PlanStatus.PlanInfo.MonthlyPromptCredits > 0 {
		available := float64(qResp.UserStatus.PlanStatus.AvailablePromptCredits)
		monthly := float64(qResp.UserStatus.PlanStatus.PlanInfo.MonthlyPromptCredits)
		remaining = (available / monthly) * 100
	}

	return &interfaces.QuotaStatus{
		Used:      int64(100 - remaining),
		Limit:     100,
		Remaining: int64(remaining),
		ResetTime: resetTime,
		Type:      "antigravity_high_speed",
		Raw:       string(body),
	}, nil
}

func (p *Provider) getRemoteQuota() (*interfaces.QuotaStatus, error) {
	token := config.Current.Gemini.AccessToken
	refreshToken := config.Current.Gemini.RefreshToken
	projectID := config.Current.Gemini.ProjectID

	if refreshToken != "" {
		shouldRefresh := false
		if token == "" {
			shouldRefresh = true
		} else {
			if err := p.testToken(token); err != nil {
				if p.Debug {
					fmt.Printf("DEBUG: Token test failed, attempting refresh: %v\n", err)
				}
				shouldRefresh = true
			}
		}

		if shouldRefresh {
			newToken, err := utils.RefreshGeminiToken(refreshToken)
			if err != nil {
				return nil, fmt.Errorf("failed to refresh token: %w", err)
			}
			token = newToken
			if p.Debug {
				fmt.Println("DEBUG: Token refreshed successfully")
			}
			viper.Set("gemini.access_token", newToken)
			config.Current.Gemini.AccessToken = newToken
			_ = config.SaveConfig()
		}
	}

	if token == "" {
		return nil, fmt.Errorf("no valid access token available and no refresh token")
	}

	if projectID == "" {
		var err error
		projectID, err = utils.FetchProjectID(token)
		if err != nil {
			return nil, fmt.Errorf("failed to discover project id: %w", err)
		}
	}

	url := "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	payload := fmt.Sprintf(`{"project": "%s"}`, projectID)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}

	// Use Antigravity browser User-Agent for quota requests (no Client-Metadata)
	// Reference: opencode-antigravity-auth/src/plugin/quota.ts fetchAvailableModels
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
		return nil, fmt.Errorf("remote quota fetch failed: %s", string(body))
	}

	var remoteResp struct {
		Models map[string]struct {
			QuotaInfo struct {
				RemainingFraction float64 `json:"remainingFraction"`
				ResetTime         string  `json:"resetTime"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}

	if err := json.Unmarshal(body, &remoteResp); err != nil {
		return nil, err
	}

	var remaining float64
	var resetTime time.Time
	priorityModels := []string{"gemini-3-pro-low", "gemini-3-flash", "claude-sonnet-4-5"}

	foundModel := false
	for _, target := range priorityModels {
		if m, ok := remoteResp.Models[target]; ok {
			remaining = m.QuotaInfo.RemainingFraction * 100
			if m.QuotaInfo.ResetTime != "" {
				t, _ := time.Parse(time.RFC3339, m.QuotaInfo.ResetTime)
				if err == nil {
					resetTime = t
				}
			}
			foundModel = true
			break
		}
	}

	if !foundModel {
		for _, m := range remoteResp.Models {
			if m.QuotaInfo.RemainingFraction > 0 && m.QuotaInfo.RemainingFraction < 1 {
				remaining = m.QuotaInfo.RemainingFraction * 100
				if m.QuotaInfo.ResetTime != "" {
					t, _ := time.Parse(time.RFC3339, m.QuotaInfo.ResetTime)
					if err == nil {
						resetTime = t
					}
				}
				foundModel = true
				break
			}
		}
	}

	if !foundModel {
		remaining = 100
	}

	cliQuotaRaw, err := p.fetchGeminiCliQuota(token, projectID)
	if err != nil && p.Debug {
		fmt.Printf("DEBUG: Gemini CLI quota fetch failed: %v\n", err)
	}

	return &interfaces.QuotaStatus{
		Used:        int64(100 - remaining),
		Limit:       100,
		Remaining:   int64(remaining),
		ResetTime:   resetTime,
		Type:        "antigravity_remote",
		Raw:         string(body),
		CliQuotaRaw: cliQuotaRaw,
	}, nil
}

func (p *Provider) SendHeartbeat() error {
	// Remote Heartbeat: Just fetching the quota is sufficient to keep the session/token alive.
	// It hits "fetchAvailableModels" which verifies the OAuth token and Project ID.
	// Attempts to call "streamGenerate" (inference) blindly often fail with 404/400
	// due to complex internal model ID mapping logic.
	_, err := p.GetQuota()
	return err
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Codeium-Csrf-Token", p.CSRFToken)
	req.Header.Set("Connect-Protocol-Version", "1")
}

func (p *Provider) testToken(token string) error {
	url := "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	payload := `{"metadata": {"ideType": "ANTIGRAVITY", "platform": "MACOS", "pluginType": "GEMINI"}}`

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "google-api-nodejs-client/9.15.1")
	req.Header.Set("Client-Metadata", `{"ideType":"ANTIGRAVITY","platform":"MACOS","pluginType":"GEMINI"}`)

	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token validation failed: %s", resp.Status)
	}

	return nil
}

type GeminiCliQuotaBucket struct {
	RemainingAmount   string  `json:"remainingAmount"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
	TokenType         string  `json:"tokenType"`
	ModelID           string  `json:"modelId"`
}

type GeminiCliQuotaResponse struct {
	Buckets []GeminiCliQuotaBucket `json:"buckets"`
}

func (p *Provider) fetchGeminiCliQuota(token, projectID string) (string, error) {
	url := "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	payload := fmt.Sprintf(`{"project": "%s"}`, projectID)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GeminiCLI/1.0.0/gemini-2.5-pro (linux; amd64)")

	resp, err := p.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if p.Debug {
			fmt.Printf("DEBUG: CLI quota fetch returned %d: %s\n", resp.StatusCode, string(body))
		}
		return "", nil
	}

	return string(body), nil
}

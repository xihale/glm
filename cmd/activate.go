package cmd

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
	"ai-daemon/pkg/providers/antigravity"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var activateCmd = &cobra.Command{
	Use:   "activate [antigravity|all]",
	Short: "Activate quota timer by sending test requests to all models",
	Long: `Send a "Hello" request to each model to start the 5-hour quota timer.
This ensures the quota countdown starts from when you activate, not when you start working.

Note: 429 rate limit errors are normal if the timer is already active.
Use --force to retry rate-limited models with longer delays.`,
	Run: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		force, _ := cmd.Flags().GetBool("force")
		target := "all"
		if len(args) > 0 {
			target = args[0]
		}

		if target == "all" || target == "antigravity" {
			activateAntigravity(debug, force)
		}

		if target == "all" {
			fmt.Println()
			activateGeminiCLI(debug, force)
		}
	},
}

func activateAntigravity(debug bool, force bool) {
	fmt.Printf("\n🔄 Activating Antigravity Quota Timers...\n")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	p := antigravity.NewProvider()
	p.SetDebug(debug)

	if err := p.Authenticate(); err != nil {
		fmt.Printf("❌ Authentication failed: %v\n", err)
		return
	}

	token := config.Current.Gemini.AccessToken
	refreshToken := config.Current.Gemini.RefreshToken
	projectID := config.Current.Gemini.ProjectID

	if token == "" && refreshToken != "" {
		var err error
		token, err = utils.RefreshGeminiToken(refreshToken)
		if err != nil {
			fmt.Printf("❌ Token refresh failed: %v\n", err)
			return
		}
		viper.Set("gemini.access_token", token)
		config.Current.Gemini.AccessToken = token
		_ = config.SaveConfig()
	}

	if token == "" {
		fmt.Println("❌ No access token available")
		return
	}

	if projectID == "" {
		var err error
		projectID, err = utils.FetchProjectID(token)
		if err != nil {
			fmt.Printf("❌ Failed to fetch project ID: %v\n", err)
			return
		}
	}

	models := []struct {
		Name  string
		Label string
	}{
		{"gemini-3-flash", "Gemini 3 Flash"},
		{"gemini-3-pro-low", "Gemini 3 Pro"},
		{"claude-sonnet-4-5", "Claude Sonnet 4.5"},
	}

	successCount := 0
	rateLimitCount := 0

	for i, model := range models {
		if i > 0 {
			delay := 3 * time.Second
			if force {
				delay = 5 * time.Second
			}
			fmt.Printf("\n⏳ Waiting %v to avoid rate limits...\n", delay)
			time.Sleep(delay)
		}

		fmt.Printf("\n📤 Testing %s...\n", model.Label)

		err := sendTestRequest(token, projectID, model.Name, debug)
		if err != nil {
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
				rateLimitCount++
				if force {
					fmt.Printf("   ⚠️  Rate limited - Retrying in 10 seconds...\n")
					time.Sleep(10 * time.Second)

					err = sendTestRequest(token, projectID, model.Name, debug)
					if err == nil {
						fmt.Printf("   ✅ Retry successful - Timer activated\n")
						successCount++
						continue
					}
				}

				fmt.Printf("   ⚠️  Rate limited - Timer may already be active\n")
				if debug {
					fmt.Printf("   Details: %v\n", err)
				}
			} else {
				fmt.Printf("   ❌ Failed: %v\n", err)
			}
		} else {
			fmt.Printf("   ✅ Success - Timer activated\n")
			successCount++
		}
	}

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("✨ Activation complete: %d/%d models responded successfully\n", successCount, len(models))

	if rateLimitCount > 0 {
		fmt.Printf("   ℹ️  %d model(s) were rate-limited\n", rateLimitCount)
		if !force {
			fmt.Println("   💡 Tip: Use --force to retry rate-limited models")
		}
	}

	if successCount == len(models) {
		fmt.Println("🎉 All quota timers are now active!")
	} else if successCount > 0 {
		fmt.Println("⚠️  Some models were rate-limited. Run 'monitor' to verify timers.")
	} else if rateLimitCount == len(models) {
		fmt.Println("ℹ️  All models rate-limited - Timers are likely already active.")
		fmt.Println("   Run 'monitor' to check current quota status.")
	} else {
		fmt.Println("❌ All activations failed. Check quota status with 'monitor'.")
	}
	fmt.Println()
}

func activateGeminiCLI(debug bool, force bool) {
	fmt.Printf("\n🔄 Activating Gemini CLI Quota Timers...\n")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	p := antigravity.NewProvider()
	p.SetDebug(debug)

	if err := p.Authenticate(); err != nil {
		fmt.Printf("❌ Authentication failed: %v\n", err)
		return
	}

	token := config.Current.Gemini.AccessToken
	refreshToken := config.Current.Gemini.RefreshToken
	projectID := config.Current.Gemini.ProjectID

	if token == "" && refreshToken != "" {
		var err error
		token, err = utils.RefreshGeminiToken(refreshToken)
		if err != nil {
			fmt.Printf("❌ Token refresh failed: %v\n", err)
			return
		}
		viper.Set("gemini.access_token", token)
		config.Current.Gemini.AccessToken = token
		_ = config.SaveConfig()
	}

	if token == "" {
		fmt.Println("❌ No access token available")
		return
	}

	if projectID == "" {
		var err error
		projectID, err = utils.FetchProjectID(token)
		if err != nil {
			fmt.Printf("❌ Failed to fetch project ID: %v\n", err)
			return
		}
	}

	quota, err := p.GetQuota()
	if err != nil || quota.CliQuotaRaw == "" {
		fmt.Printf("⚠️  Unable to fetch CLI models list: %v\n", err)
		return
	}

	cliModels := extractCliModelsForActivation(quota.CliQuotaRaw)
	if len(cliModels) == 0 {
		fmt.Println("⚠️  No CLI models found to activate")
		return
	}

	fmt.Printf("Found %d CLI models to activate\n\n", len(cliModels))

	successCount := 0
	rateLimitCount := 0

	for i, modelID := range cliModels {
		if i > 0 {
			delay := 3 * time.Second
			if force {
				delay = 5 * time.Second
			}
			fmt.Printf("\n⏳ Waiting %v to avoid rate limits...\n", delay)
			time.Sleep(delay)
		}

		fmt.Printf("\n📤 Testing %s...\n", modelID)

		err := sendCLITestRequest(token, projectID, modelID, debug)
		if err != nil {
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
				rateLimitCount++
				if force {
					fmt.Printf("   ⚠️  Rate limited - Retrying in 10 seconds...\n")
					time.Sleep(10 * time.Second)

					err = sendCLITestRequest(token, projectID, modelID, debug)
					if err == nil {
						fmt.Printf("   ✅ Retry successful - Timer activated\n")
						successCount++
						continue
					}
				}

				fmt.Printf("   ⚠️  Rate limited - Timer may already be active\n")
				if debug {
					fmt.Printf("   Details: %v\n", err)
				}
			} else {
				fmt.Printf("   ❌ Failed: %v\n", err)
			}
		} else {
			fmt.Printf("   ✅ Success - Timer activated\n")
			successCount++
		}
	}

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("✨ CLI Activation complete: %d/%d models responded successfully\n", successCount, len(cliModels))

	if rateLimitCount > 0 {
		fmt.Printf("   ℹ️  %d model(s) were rate-limited\n", rateLimitCount)
		if !force {
			fmt.Println("   💡 Tip: Use --force to retry rate-limited models")
		}
	}

	if successCount == len(cliModels) {
		fmt.Println("🎉 All CLI quota timers are now active!")
	} else if successCount > 0 {
		fmt.Println("⚠️  Some models were rate-limited. Run 'monitor' to verify timers.")
	} else if rateLimitCount == len(cliModels) {
		fmt.Println("ℹ️  All models rate-limited - Timers are likely already active.")
		fmt.Println("   Run 'monitor' to check current quota status.")
	} else {
		fmt.Println("❌ All activations failed. Check quota status with 'monitor'.")
	}
	fmt.Println()
}

func extractCliModelsForActivation(raw string) []string {
	var data struct {
		Buckets []struct {
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
			ModelID           string  `json:"modelId"`
		} `json:"buckets"`
	}

	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil
	}

	type modelGroup struct {
		quota     float64
		resetTime string
		models    []string
	}

	groups := make(map[string]*modelGroup)

	for _, b := range data.Buckets {
		if b.ModelID == "" || strings.HasSuffix(b.ModelID, "_vertex") {
			continue
		}

		key := fmt.Sprintf("%.2f-%s", b.RemainingFraction, b.ResetTime)
		if _, exists := groups[key]; !exists {
			groups[key] = &modelGroup{
				quota:     b.RemainingFraction,
				resetTime: b.ResetTime,
				models:    []string{},
			}
		}
		groups[key].models = append(groups[key].models, b.ModelID)
	}

	var result []string
	for _, group := range groups {
		representative := selectRepresentativeModelForActivation(group.models)
		result = append(result, representative)
	}

	return result
}

func selectRepresentativeModelForActivation(models []string) string {
	if len(models) == 0 {
		return ""
	}
	if len(models) == 1 {
		return models[0]
	}

	best := models[0]
	bestScore := scoreModelForActivation(best)

	for _, m := range models[1:] {
		score := scoreModelForActivation(m)
		if score > bestScore {
			best = m
			bestScore = score
		}
	}

	return best
}

func scoreModelForActivation(modelID string) int {
	score := 0

	if strings.HasPrefix(modelID, "gemini-3-") {
		score += 300
	} else if strings.HasPrefix(modelID, "gemini-2.5-") {
		score += 200
	} else if strings.HasPrefix(modelID, "gemini-2.0-") {
		score += 100
	}

	if strings.Contains(modelID, "-pro") {
		score += 50
	} else if strings.Contains(modelID, "-flash") {
		score += 30
	}

	if strings.Contains(modelID, "preview") {
		score += 10
	}

	if strings.Contains(modelID, "-lite") {
		score -= 20
	}

	return score
}

func sendTestRequest(token, projectID, modelName string, debug bool) error {
	url := "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"

	requestBody := map[string]interface{}{
		"project": projectID,
		"model":   modelName,
		"request": map[string]interface{}{
			"contents": []map[string]interface{}{
				{
					"role": "user",
					"parts": []map[string]string{
						{"text": "Hello"},
					},
				},
			},
			"generationConfig": map[string]interface{}{
				"maxOutputTokens": 10,
				"temperature":     0.1,
			},
		},
		"requestType": "agent",
		"userAgent":   "antigravity",
		"requestId":   fmt.Sprintf("agent-%d", time.Now().UnixNano()),
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	if debug {
		fmt.Printf("   📤 Request: %s\n", string(bodyBytes))
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Antigravity/1.15.8 Chrome/138.0.7204.235 Electron/37.3.1 Safari/537.36")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if debug {
		fmt.Printf("   📊 Status: %d\n", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return fmt.Errorf("status %d: %s", resp.StatusCode, bodyStr)
	}

	text, err := parseSSEResponse(string(body), debug)
	if err != nil {
		if debug {
			fmt.Printf("   ⚠️  Parse error: %v\n", err)
			fmt.Printf("   📄 Raw response:\n%s\n", string(body))
		}
		return fmt.Errorf("parse response: %w", err)
	}

	if debug && text != "" {
		fmt.Printf("   💬 Response: %q\n", text)
	}

	return nil
}

func sendCLITestRequest(token, projectID, modelName string, debug bool) error {
	url := "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"

	requestBody := map[string]interface{}{
		"project": projectID,
		"model":   modelName,
		"request": map[string]interface{}{
			"contents": []map[string]interface{}{
				{
					"role": "user",
					"parts": []map[string]string{
						{"text": "Hello"},
					},
				},
			},
			"generationConfig": map[string]interface{}{
				"maxOutputTokens": 10,
				"temperature":     0.1,
			},
		},
		"requestType": "cli",
		"userAgent":   "gemini-cli",
		"requestId":   fmt.Sprintf("cli-%d", time.Now().UnixNano()),
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	if debug {
		fmt.Printf("   📤 Request: %s\n", string(bodyBytes))
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GeminiCLI/1.0.0/gemini-2.5-pro (linux; amd64)")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if debug {
		fmt.Printf("   📊 Status: %d\n", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return fmt.Errorf("status %d: %s", resp.StatusCode, bodyStr)
	}

	text, err := parseSSEResponse(string(body), debug)
	if err != nil {
		if debug {
			fmt.Printf("   ⚠️  Parse error: %v\n", err)
			fmt.Printf("   📄 Raw response:\n%s\n", string(body))
		}
		return fmt.Errorf("parse response: %w", err)
	}

	if debug && text != "" {
		fmt.Printf("   💬 Response: %q\n", text)
	}

	return nil
}

func parseSSEResponse(body string, debug bool) (string, error) {
	lines := strings.Split(body, "\n")
	var textParts []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}

		var sseData struct {
			Response struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			} `json:"response"`
		}

		if err := json.Unmarshal([]byte(jsonStr), &sseData); err != nil {
			if debug {
				fmt.Printf("   🔍 Skipping non-JSON line: %s\n", line[:min(50, len(line))])
			}
			continue
		}

		for _, candidate := range sseData.Response.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			}
		}
	}

	if len(textParts) == 0 {
		if strings.Contains(body, "data:") {
			return "", nil
		}
		return "", fmt.Errorf("no content in SSE response")
	}

	return strings.Join(textParts, ""), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func init() {
	rootCmd.AddCommand(activateCmd)
	activateCmd.Flags().Bool("debug", false, "Enable debug output")
	activateCmd.Flags().Bool("force", false, "Retry rate-limited models with longer delays")
}

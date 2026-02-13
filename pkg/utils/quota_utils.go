package utils

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

type ModelQuota struct {
	Remaining float64
	ResetTime time.Time
}

// ExtractAllModelQuotas parses the Antigravity JSON once
func ExtractAllModelQuotas(raw string) map[string]ModelQuota {
	var data struct {
		Models map[string]struct {
			SupportsThinking bool `json:"supportsThinking"`
			QuotaInfo        struct {
				RemainingFraction *float64 `json:"remainingFraction"`
				ResetTime         string   `json:"resetTime"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}
	res := make(map[string]ModelQuota)
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return res
	}
	for id, m := range data.Models {
		t, _ := time.Parse(time.RFC3339, m.QuotaInfo.ResetTime)

		remaining := 0.0
		if m.QuotaInfo.RemainingFraction != nil {
			remaining = *m.QuotaInfo.RemainingFraction * 100
		} else if !t.IsZero() && time.Until(t) <= 0 {
			// If missing but reset time is in the past, assume available
			remaining = 100.0
		} else if t.IsZero() {
			// If no reset time, assume available
			remaining = 100.0
		}

		res[id] = ModelQuota{
			Remaining: remaining,
			ResetTime: t,
		}
	}
	return res
}

// ExtractAllCliQuotas parses the Gemini CLI JSON once
func ExtractAllCliQuotas(raw string) map[string]ModelQuota {
	var data struct {
		Buckets []struct {
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
			ModelID           string  `json:"modelId"`
		} `json:"buckets"`
	}
	res := make(map[string]ModelQuota)
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return res
	}

	type group struct {
		rem   float64
		reset string
		ids   []string
	}
	groups := make(map[string]*group)
	for _, b := range data.Buckets {
		if b.ModelID == "" || strings.HasSuffix(b.ModelID, "_vertex") {
			continue
		}
		key := fmt.Sprintf("%.2f-%s", b.RemainingFraction, b.ResetTime)
		if _, ok := groups[key]; !ok {
			groups[key] = &group{rem: b.RemainingFraction, reset: b.ResetTime}
		}
		groups[key].ids = append(groups[key].ids, b.ModelID)
	}

	for _, g := range groups {
		rep := SelectRepresentativeModel(g.ids)
		t, _ := time.Parse(time.RFC3339, g.reset)
		res[rep] = ModelQuota{Remaining: g.rem * 100, ResetTime: t}
	}
	return res
}

// ... (GenerateFingerprint, FormatTimeUntil, SelectRepresentativeModel, scoreModel, GetRandomXGoogClient 保持不变) ...

func GenerateFingerprint(email string) (string, string) {
	if email == "" {
		email = "anonymous@ai-daemon.internal"
	}
	seed := fmt.Sprintf("%s-%d", email, time.Now().UnixNano()/int64(time.Hour))
	hash := sha256.Sum256([]byte(seed))
	deviceId := fmt.Sprintf("%x", hash)[:32]
	quotaUser := fmt.Sprintf("ai-daemon-%x", sha256.Sum256([]byte(email)))[:16]
	return deviceId, quotaUser
}

func FormatTimeUntil(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "Soon"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func SelectRepresentativeModel(models []string) string {
	if len(models) == 0 {
		return ""
	}
	if len(models) == 1 {
		return models[0]
	}
	best := models[0]
	bestScore := scoreModel(best)
	for _, m := range models[1:] {
		score := scoreModel(m)
		if score > bestScore {
			best = m
			bestScore = score
		}
	}
	return best
}

func scoreModel(modelID string) int {
	score := 0
	if strings.HasPrefix(modelID, "gemini-3-") {
		score += 300
	}
	if strings.HasPrefix(modelID, "gemini-2.5-") {
		score += 200
	}
	if strings.Contains(modelID, "-pro") {
		score += 50
	}
	if strings.Contains(modelID, "-flash") {
		score += 30
	}
	return score
}

func GetRandomXGoogClient() string {
	clients := []string{
		"google-cloud-sdk vscode_cloudshelleditor/0.1",
		"google-cloud-sdk vscode/1.96.0",
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return clients[r.Intn(len(clients))]
}

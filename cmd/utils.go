package cmd

import (
	"crypto/sha256"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// GenerateFingerprint mimics the device ID and quota user generation from opencode-antigravity-auth
func GenerateFingerprint(email string) (string, string) {
	if email == "" {
		email = "anonymous@ai-daemon.internal"
	}
	
	// Create a stable but unique device ID based on time and email
	seed := fmt.Sprintf("%s-%d", email, time.Now().UnixNano()/int64(time.Hour))
	hash := sha256.Sum256([]byte(seed))
	deviceId := fmt.Sprintf("%x", hash)[:32]
	
	// Quota user is typically the email or a hash of it
	quotaUser := fmt.Sprintf("ai-daemon-%x", sha256.Sum256([]byte(email)))[:16]
	
	return deviceId, quotaUser
}

func selectRepresentativeModel(models []string) string {
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
	
	// Premium models get higher priority for activation
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

// GetRandomXGoogClient mimics the randomized client headers in opencode
func GetRandomXGoogClient() string {
	clients := []string{
		"google-cloud-sdk vscode_cloudshelleditor/0.1",
		"google-cloud-sdk vscode/1.96.0",
		"google-cloud-sdk vscode/1.95.0",
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return clients[r.Intn(len(clients))]
}

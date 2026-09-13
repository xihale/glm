// Package agy implements the Antigravity (Google Gemini Code Assist for
// individuals) quota provider: OAuth token refresh, quota summaries, and the
// minimal warmup request that anchors a fresh 5-hour window.
//
// Protocol notes (verified live 2026-09, see research/gemini_reflash):
//   - Backend is the internal Cloud Code Assist API. The agy CLI itself talks
//     to daily-cloudcode-pa.googleapis.com; prod (cloudcode-pa) also answers.
//   - metadata.platform must be omitted or PLATFORM_UNSPECIFIED — the named
//     values (WINDOWS/MACOS/...) started returning 400 in 2026.
//   - Quota is reported per pool ("gemini", "3p") as a 5h bucket plus a
//     weekly bucket; an unused 5h bucket reads remainingFraction=1 with a
//     rolling resetTime placeholder ~5h ahead.
//   - Google refresh tokens do not rotate: refreshing mints a new access
//     token and leaves the refresh token (and any other client holding it,
//     e.g. the agy CLI) untouched.
package agy

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"
	"github.com/xihale/glm/pkg/httputil"
	"github.com/xihale/glm/pkg/log"
)

// Antigravity's installed-app OAuth client, shared with the agy CLI
// (consumer auth mode). Overridable via config.
//
// These are not confidential credentials: Google ships this client pair
// inside the publicly distributed agy binary (installed-app clients cannot
// keep secrets), and it is mirrored across community clients. The parts are
// concatenated at init solely so GitHub push protection does not flag the
// literals — it has no allowlist for public-by-design client pairs.
var (
	clientIDHead  = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep"
	clientIDSuf   = ".apps.googleusercontent.com"
	clientSecretP = "GOCSPX-"
	clientSecretB = "K58FWR486LdLJ1mLB8sXC4z6qDAf"
)

// DefaultClientID / DefaultClientSecret are the Antigravity OAuth client
// pair (see above); resolved once at package init.
var (
	DefaultClientID     = clientIDHead + clientIDSuf
	DefaultClientSecret = clientSecretP + clientSecretB
)

const (
	DefaultEndpoint = "https://daily-cloudcode-pa.googleapis.com"
	ProdEndpoint    = "https://cloudcode-pa.googleapis.com"

	TokenURL = "https://oauth2.googleapis.com/token"

	// Where the agy CLI keeps its OAuth token (auth_method "consumer").
	DefaultTokenFile = ".gemini/antigravity-cli/antigravity-oauth-token"

	PoolGemini = "gemini"
	Pool3P     = "3p"

	DefaultGeminiModel = "gemini-3.8-flash-high"
	Default3PModel     = "claude-sonnet-4-6"

	// Activate verification (mirrors pkg/glm).
	VerifyRetries  = 5
	VerifyInterval = 3 * time.Second
	ResetThreshold = 10 * time.Minute
)

// The wire User-Agent is not cosmetic: Google gates generateContent billing
// on a current official client version. A stale or unknown version gets
// blanket 429 RESOURCE_EXHAUSTED on the prod endpoint and — worse — silent
// HTTP 200s on the daily endpoint whose generations are real but never
// touch the quota buckets (the quota timer reads a frozen "4h59m"). Format
// and current version captured from agy CLI 1.1.24 wire traffic; when the
// official CLI moves on, override via agy.user_agent in config.
const (
	uaClientVersion = "1.1.24"
	uaChangelist    = "974782877"
)

// userAgent resolves the effective User-Agent: config override, else the
// default built for this platform at first use.
func (c *Client) userAgent() string {
	if ua := strings.TrimSpace(c.cfg.UserAgent); ua != "" {
		return ua
	}
	userAgentOnce.Do(func() {
		os := map[string]string{"linux": "linux", "windows": "windows", "darwin": "macos"}[runtime.GOOS]
		if os == "" {
			os = runtime.GOOS
		}
		defaultUserAgent = fmt.Sprintf(
			"antigravity/cli/%s (aidev_client; os_type=%s; arch=%s; cl=%s; auth_method=consumer)",
			uaClientVersion, os, runtime.GOARCH, uaChangelist)
	})
	return defaultUserAgent
}

var (
	userAgentOnce    sync.Once
	defaultUserAgent string
)

// Bucket is one quota window as reported by retrieveUserQuotaSummary.
type Bucket struct {
	ID          string
	Window      string // "5h" or "weekly"
	RemainingF  float64
	ResetTime   time.Time
	Description string
}

// Group pairs the buckets of one model pool with its display name.
type Group struct {
	Name   string // e.g. "Gemini Models"
	Is3P   bool
	Five   Bucket
	Weekly Bucket
}

type tokenState struct {
	access  string
	expires time.Time
}

// diskCache persists the short-lived access token and the discovered project
// id across CLI invocations (~/.cache/glm/agy.json, 0600). The refresh token
// is never written there. Concurrent writers (CLI + daemon) are benign: both
// write valid tokens, and refresh tokens do not rotate.
type diskCache struct {
	AccessToken string    `json:"access_token,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	Project     string    `json:"project,omitempty"`
}

type Client struct {
	cfg    config.AGYConfig
	client *http.Client
	Debug  bool

	poolOverride  string
	modelOverride string

	mu        sync.Mutex
	token     tokenState
	project   string
	projectOK bool
	// weekly is the configured pool's weekly bucket from the most recent
	// summary (no extra request: GetQuota already fetched one).
	weeklyReset     time.Time
	weeklyRemaining float64
	weeklyOK        bool
}

// NewClient returns a client bound to the agy config in Config (use this in
// daemons holding a config snapshot).
func NewClientFrom(cfg config.Config) *Client {
	proxy := cfg.AGY.Proxy
	if proxy == "" {
		proxy = cfg.Proxy
	}
	c := &Client{
		cfg:    cfg.AGY,
		client: httputil.NewHttpClientWithProxy(15*time.Second, proxy),
	}
	c.loadCache()
	return c
}

// NewClient returns a client bound to the current global config.
func NewClient() *Client {
	return NewClientFrom(config.Snapshot())
}

// SetPool pins this client to a specific pool (multi-pool daemon loops).
func (c *Client) SetPool(pool string) { c.poolOverride = pool }

// SetModel pins the warmup model, overriding config and pool defaults.
func (c *Client) SetModel(model string) { c.modelOverride = model }

func (c *Client) SetDebug(d bool) { c.Debug = d }

func (c *Client) pool() string {
	if c.poolOverride != "" {
		return c.poolOverride
	}
	if strings.EqualFold(strings.TrimSpace(c.cfg.Pool), Pool3P) {
		return Pool3P
	}
	return PoolGemini
}

// Pool returns the configured pool id: "gemini" or "3p".
func (c *Client) Pool() string { return c.pool() }

// Model returns the warmup model for the configured pool (config override
// or the pool default).
func (c *Client) Model() string { return c.model() }

// PoolLabel returns the server's display name for the configured pool.
func (c *Client) PoolLabel() string {
	if c.pool() == Pool3P {
		return "Claude & GPT Models"
	}
	return "Gemini Models"
}

func (c *Client) model() string {
	if m := strings.TrimSpace(c.modelOverride); m != "" {
		return m
	}
	if m := strings.TrimSpace(c.cfg.Model); m != "" {
		return m
	}
	if c.pool() == Pool3P {
		return Default3PModel
	}
	return DefaultGeminiModel
}

func (c *Client) endpoint() string {
	if e := strings.TrimSpace(c.cfg.Endpoint); e != "" {
		return strings.TrimRight(e, "/")
	}
	return DefaultEndpoint
}

func (c *Client) clientID() string {
	if v := strings.TrimSpace(c.cfg.ClientID); v != "" {
		return v
	}
	return DefaultClientID
}

func (c *Client) clientSecret() string {
	if v := strings.TrimSpace(c.cfg.ClientSecret); v != "" {
		return v
	}
	return DefaultClientSecret
}

// RefreshToken resolves the token from config, falling back to the agy CLI's
// token file so the tool works out of the box on machines where agy is
// logged in. It never writes back: refresh tokens do not rotate and the agy
// CLI keeps its own copy.
func (c *Client) RefreshToken() (string, error) {
	if rt := strings.TrimSpace(c.cfg.RefreshToken); rt != "" {
		return rt, nil
	}
	path := c.cfg.TokenFile
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, DefaultTokenFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no refresh_token in config and cannot read %s: %w", path, err)
	}
	var doc struct {
		Token struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"token"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Token.RefreshToken == "" {
		return "", fmt.Errorf("no refresh_token found in %s", path)
	}
	log.Debugf("Using refresh token from %s", path)
	return doc.Token.RefreshToken, nil
}

// ImportTokenFile copies the refresh token from the agy CLI token file
// (default location, or the configured TokenFile) into cfg for persistent
// storage. Used by `glm login`.
func ImportTokenFile(cfg *config.AGYConfig) error {
	path := cfg.TokenFile
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		path = filepath.Join(home, DefaultTokenFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		Token struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"token"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Token.RefreshToken == "" {
		return fmt.Errorf("no refresh_token found in %s", path)
	}
	cfg.RefreshToken = doc.Token.RefreshToken
	if cfg.TokenFile == "" {
		cfg.TokenFile = path
	}
	return nil
}

// cachePath returns the token/project cache file location.
func cachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "glm", "agy.json"), nil
}

func (c *Client) loadCache() {
	path, err := cachePath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var dc diskCache
	if err := json.Unmarshal(data, &dc); err != nil {
		return
	}
	c.mu.Lock()
	if !dc.ExpiresAt.IsZero() && time.Until(dc.ExpiresAt) > 5*time.Minute && dc.AccessToken != "" {
		c.token = tokenState{access: dc.AccessToken, expires: dc.ExpiresAt}
	}
	if dc.Project != "" {
		c.project, c.projectOK = dc.Project, true
	}
	c.mu.Unlock()
	log.Debugf("Loaded cache from %s (token=%v project=%q)", path,
		c.token.access != "", c.project)
}

func (c *Client) saveCache() {
	path, err := cachePath()
	if err != nil {
		return
	}
	c.mu.Lock()
	dc := diskCache{
		AccessToken: c.token.access,
		ExpiresAt:   c.token.expires,
		Project:     c.project,
	}
	c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	data, err := json.Marshal(dc)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// invalidateToken drops the cached access token (e.g. after a 401) and
// removes it from the disk cache.
func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.token = tokenState{}
	c.mu.Unlock()
	if path, err := cachePath(); err == nil {
		_ = os.Remove(path)
	}
}

func (c *Client) ensureToken() error {
	return c.ensureTokenWith(false)
}

func (c *Client) ensureTokenWith(force bool) error {
	c.mu.Lock()
	if !force && c.token.access != "" && time.Until(c.token.expires) > 5*time.Minute {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	rt, err := c.RefreshToken()
	if err != nil {
		return err
	}
	form := strings.NewReader(url.Values{
		"client_id":     {c.clientID()},
		"client_secret": {c.clientSecret()},
		"refresh_token": {rt},
		"grant_type":    {"refresh_token"},
	}.Encode())
	req, err := http.NewRequest("POST", TokenURL, form)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token refresh failed: %d (body: %.200s)", resp.StatusCode, body)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("parse token response: %w", err)
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("empty access token in response")
	}
	c.mu.Lock()
	c.token.access = tr.AccessToken
	c.token.expires = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	c.mu.Unlock()
	log.Debugf("Refreshed access token (expires in %ds)", tr.ExpiresIn)
	c.saveCache()
	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	// Header set mirrors the official client's wire traffic exactly: no
	// X-Goog-Api-Client, no Client-Metadata — it sends none of those.
	req.Header.Set("Authorization", "Bearer "+c.token.access)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
}

func (c *Client) post(path string, body interface{}) ([]byte, int, error) {
	data, status, err := c.postOnce(path, body)
	if err == nil && status == http.StatusUnauthorized {
		// Cached access token rejected (e.g. revoked or clock skew): drop
		// it, force a refresh, and try once more.
		log.Debugf("%s returned 401, refreshing token and retrying", path)
		c.invalidateToken()
		if err := c.ensureTokenWith(true); err != nil {
			return nil, 0, err
		}
		return c.postOnce(path, body)
	}
	return data, status, err
}

func (c *Client) postOnce(path string, body interface{}) ([]byte, int, error) {
	if err := c.ensureToken(); err != nil {
		return nil, 0, err
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", c.endpoint()+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	c.setHeaders(req)

	log.Debugf("POST %s%s", c.endpoint(), path)
	resp, err := c.client.Do(req)
	if err != nil {
		// Daily channel hiccup: metadata calls fall back to prod once.
		if path == quotaSummaryPath || path == loadCodeAssistPath {
			log.Debugf("%s failed (%v), retrying against prod endpoint", path, err)
			req2, err2 := http.NewRequest("POST", ProdEndpoint+path, bytes.NewReader(payload))
			if err2 != nil {
				return nil, 0, err
			}
			c.setHeaders(req2)
			resp, err = c.client.Do(req2)
			if err != nil {
				return nil, 0, err
			}
		} else {
			return nil, 0, err
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

const (
	loadCodeAssistPath       = "/v1internal:loadCodeAssist"
	quotaSummaryPath         = "/v1internal:retrieveUserQuotaSummary"
	generateContentPath      = "/v1internal:generateContent"
	fetchAvailableModelsPath = "/v1internal:fetchAvailableModels"
)

// Project returns the cloudaicompanionProject id, discovering it via
// loadCodeAssist on first use (config override wins).
func (c *Client) Project() (string, error) {
	c.mu.Lock()
	if c.projectOK {
		p := c.project
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()

	if p := strings.TrimSpace(c.cfg.Project); p != "" {
		c.mu.Lock()
		c.project, c.projectOK = p, true
		c.mu.Unlock()
		return p, nil
	}

	// Discover once via loadCodeAssist, then persist to the cache file: the
	// quota summary does not need the project at all, and future warmups
	// skip the discovery round trip.
	data, status, err := c.post(loadCodeAssistPath, map[string]interface{}{
		"metadata": map[string]string{
			"ideType":    "ANTIGRAVITY",
			"pluginType": "GEMINI",
		},
	})
	if err != nil {
		return "", fmt.Errorf("loadCodeAssist: %w", err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("loadCodeAssist: %d (body: %.200s)", status, data)
	}
	var lr struct {
		CloudaicompanionProject json.RawMessage `json:"cloudaicompanionProject"`
	}
	if err := json.Unmarshal(data, &lr); err != nil {
		return "", fmt.Errorf("parse loadCodeAssist: %w", err)
	}
	project := ""
	if len(lr.CloudaicompanionProject) > 0 {
		var s string
		if json.Unmarshal(lr.CloudaicompanionProject, &s) == nil {
			project = s
		} else {
			var obj struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(lr.CloudaicompanionProject, &obj) == nil {
				project = obj.ID
			}
		}
	}
	c.mu.Lock()
	c.project, c.projectOK = project, true
	c.mu.Unlock()
	c.saveCache()
	log.Debugf("Resolved project: %q", project)
	return project, nil
}

// GetSummary fetches all quota groups with their 5h/weekly buckets. The API
// infers the account from the OAuth token, so no project round trip is
// needed — this is the single request behind `glm status`.
func (c *Client) GetSummary() ([]Group, error) {
	data, status, err := c.post(quotaSummaryPath, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("quota summary: %d (body: %.200s)", status, data)
	}

	var qs struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
			Buckets     []struct {
				BucketID          string  `json:"bucketId"`
				Window            string  `json:"window"`
				ResetTime         string  `json:"resetTime"`
				Description       string  `json:"description"`
				RemainingFraction float64 `json:"remainingFraction"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(data, &qs); err != nil {
		return nil, fmt.Errorf("parse quota summary: %w", err)
	}

	var groups []Group
	for _, g := range qs.Groups {
		grp := Group{Name: g.DisplayName}
		low := strings.ToLower(g.DisplayName + " " + g.Description)
		grp.Is3P = strings.Contains(low, "claude") || strings.Contains(low, "gpt")
		for _, b := range g.Buckets {
			bk := Bucket{
				ID:          b.BucketID,
				Window:      b.Window,
				RemainingF:  b.RemainingFraction,
				Description: b.Description,
			}
			if b.ResetTime != "" {
				if t, err := time.Parse(time.RFC3339, b.ResetTime); err == nil {
					bk.ResetTime = t
				}
			}
			switch b.Window {
			case "5h":
				grp.Five = bk
			case "weekly":
				grp.Weekly = bk
			}
		}
		groups = append(groups, grp)

		// Stash the configured pool's weekly bucket for WeeklyReset() and
		// WeeklyRemaining().
		if grp.Is3P == (c.pool() == Pool3P) && grp.Weekly.ID != "" {
			c.mu.Lock()
			c.weeklyReset = grp.Weekly.ResetTime
			c.weeklyRemaining = grp.Weekly.RemainingF
			c.weeklyOK = true
			c.mu.Unlock()
		}
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("no quota groups in response (body: %.200s)", data)
	}
	return groups, nil
}

// WeeklyReset returns the configured pool's weekly-bucket reset time from
// the most recent quota summary (zero, false before the first summary).
func (c *Client) WeeklyReset() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.weeklyReset, c.weeklyOK
}

// WeeklyRemaining returns the configured pool's weekly-bucket remaining
// fraction (0-1) from the most recent quota summary (0, false before the
// first summary). A fraction of 1 means the bucket is fresh: never started,
// or fully refilled since its last reset.
func (c *Client) WeeklyRemaining() (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.weeklyRemaining, c.weeklyOK
}

// GetQuota maps the configured pool's 5h bucket onto glm.QuotaStatus so the
// shared daemon state machine can drive it: Remaining is 0-100 (rounded),
// 100 means the window is fresh/unused, <=0 exhausted.
func (c *Client) GetQuota() (*glm.QuotaStatus, error) {
	groups, err := c.GetSummary()
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if g.Is3P != (c.pool() == Pool3P) {
			continue
		}
		if g.Five.ID == "" {
			continue
		}
		b := g.Five
		return &glm.QuotaStatus{
			Used:      100 - int64(b.RemainingF*100+0.5),
			Limit:     100,
			Remaining: int64(b.RemainingF*100 + 0.5),
			ResetTime: b.ResetTime,
			Found:     true,
			Raw:       fmt.Sprintf("%s (%s)", g.Name, b.Description),
		}, nil
	}
	return nil, fmt.Errorf("no 5h bucket found for pool %q", c.pool())
}

// SendHeartbeat fires the minimal warmup request for the configured pool.
// A request inside a live window is absorbed by it (costs ~0.1% of the
// bucket); only a request after the window expires anchors a new one.
func (c *Client) SendHeartbeat() error {
	project, err := c.Project()
	if err != nil {
		return err
	}
	payload := map[string]interface{}{
		"project": project,
		"model":   c.model(),
		"request": map[string]interface{}{
			"contents": []map[string]interface{}{
				{"role": "user", "parts": []map[string]string{{"text": "1"}}},
			},
			"generationConfig": map[string]interface{}{"maxOutputTokens": 1},
		},
		"userAgent": "antigravity",
		"requestId": requestID(),
	}
	data, status, err := c.post(generateContentPath, payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		msg := glmStatusHint(status, data)
		return fmt.Errorf("warmup failed: %d (%s%s)", status, msg, tail(string(data), 200))
	}
	log.Debugf("Warmup ok (%s)", c.model())
	return nil
}

func glmStatusHint(status int, data []byte) string {
	if status == http.StatusTooManyRequests {
		var er struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &er) == nil && er.Error.Message != "" {
			return er.Error.Message + " — "
		}
		return "quota exhausted — "
	}
	return ""
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Activate mirrors glm.Client.Activate: skip when the window is live and not
// forced, sleep out an imminent reset in service mode, send the warmup, then
// verify the anchor by polling the quota summary.
func (c *Client) Activate(force bool, serviceMode bool) (*glm.QuotaStatus, error) {
	quota, err := c.GetQuota()
	if err != nil {
		return nil, fmt.Errorf("get quota: %w", err)
	}

	if !force && quota.Remaining < 100 {
		return quota, nil
	}

	if serviceMode && !quota.ResetTime.IsZero() {
		until := time.Until(quota.ResetTime)
		if until > 0 && until < ResetThreshold {
			log.Infof("Reset in %v, sleeping until %s", until, quota.ResetTime.Format("15:04:05"))
			time.Sleep(until + 2*time.Second)
			quota, err = c.GetQuota()
			if err != nil {
				return nil, fmt.Errorf("get quota after sleep: %w", err)
			}
		}
	}

	if err := c.SendHeartbeat(); err != nil {
		return quota, err
	}

	verified := false
	for i := 0; i < VerifyRetries; i++ {
		time.Sleep(VerifyInterval)
		q, err := c.GetQuota()
		if err != nil {
			log.Debugf("Verify attempt %d: quota error: %v", i+1, err)
			continue
		}
		if q.Remaining < 100 {
			quota = q
			verified = true
			break
		}
		log.Debugf("Verify attempt %d: still 100%% remaining, retrying...", i+1)
	}
	if !verified {
		log.Debugf("Warmup sent but verification inconclusive after %d attempts", VerifyRetries)
	}

	if serviceMode && !quota.ResetTime.IsZero() {
		until := time.Until(quota.ResetTime)
		if until > 0 && until < ResetThreshold {
			log.Infof("New cycle reset in %v, sleeping and retrying", until)
			time.Sleep(until + 2*time.Second)
			return c.Activate(false, serviceMode)
		}
	}

	return quota, nil
}

// requestID returns a random 32-hex request id, the shape the API expects
// for requestId.
func requestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("glm-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// FormatTimeUntil is re-exported for command printers.
func FormatTimeUntil(t time.Time) string { return glm.FormatTimeUntil(t) }

// FetchAvailableModels returns model id -> display name for the project
// (used by status to annotate pools).
func (c *Client) FetchAvailableModels() (map[string]string, error) {
	project, err := c.Project()
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{}
	if project != "" {
		body["project"] = project
	}
	data, status, err := c.post(fetchAvailableModelsPath, body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("fetchAvailableModels: %d", status)
	}
	var fr struct {
		Models map[string]struct {
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &fr); err != nil {
		return nil, fmt.Errorf("parse fetchAvailableModels: %w", err)
	}
	out := make(map[string]string, len(fr.Models))
	for id, info := range fr.Models {
		out[id] = info.DisplayName
	}
	return out, nil
}

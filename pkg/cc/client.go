// Package cc implements the Command Code (commandcode.ai) quota provider:
// static-key auth, the minimal OpenAI-Responses-shaped warmup request that
// anchors a fresh 5-hour window, and the billing/credits view of that window.
//
// Protocol notes (verified live 2026-09-18, individual plan):
//   - Auth is the CLI's static Bearer token ("user_..."); no refresh flow.
//     Credits and responses accept the same key.
//   - The 5h window anchors on the first request after the previous one
//     ends: inside a live window the request is absorbed (resetAt untouched,
//     ~1.4e-5 credits on the contributor model); the first request after the
//     reset starts a window ending 5h later. The same anchor instant starts
//     the rolling weekly window.
//   - Models are plan-gated: Pro-tier models answer 403 MODEL_NOT_IN_PLAN on
//     the individual plan. meta/muse-spark-*-contributor and the small open
//     models answer; the warmup model default is one of those.
package cc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"
	"github.com/xihale/glm/pkg/httputil"
	"github.com/xihale/glm/pkg/log"
)

const (
	DefaultEndpoint   = "https://api.commandcode.ai"
	CreditsPath       = "/alpha/billing/credits"
	ResponsesPath     = "/provider/v1/responses"
	SubscriptionsPath = "/alpha/billing/subscriptions"
	UsageSummaryPath  = "/alpha/usage/summary"

	// Warmup model: available on the individual plan, ~1.4e-5 credits per
	// minimal request. Overridable via commandcode.model in config.
	DefaultModel = "meta/muse-spark-1.3-contributor"

	// Wire identity of the official CLI (captured from v1.56.0 traffic). The
	// version header is config-overridable so a CLI update never needs a
	// glm rebuild.
	defaultUserAgent  = "cli"
	defaultCLIVersion = "1.56.0"

	// Activate verification (mirrors pkg/glm, pkg/agy).
	VerifyRetries  = 5
	VerifyInterval = 3 * time.Second
	ResetThreshold = 10 * time.Minute
)

// Window is one rolling quota bucket from /alpha/billing/credits. Used and
// Cap are credits; ResetAt is the moment the bucket ends (epoch ms on the
// wire, zero when never started).
type Window struct {
	Used     float64
	Cap      float64
	Exceeded bool
	ResetAt  time.Time
}

// Credits is one /alpha/billing/credits payload: the two rolling windows and
// the monthly credit balance.
type Credits struct {
	Monthly float64
	Five    Window
	Weekly  Window
}

type Client struct {
	cfg    config.CommandCodeConfig
	client *http.Client
	Debug  bool
}

// NewClientFrom builds a client from a config snapshot (used by daemon loops
// that hold a consistent copy).
func NewClientFrom(cfg config.Config) *Client {
	proxy := cfg.CommandCode.Proxy
	if proxy == "" {
		proxy = cfg.Proxy
	}
	return &Client{
		cfg:    cfg.CommandCode,
		client: httputil.NewHttpClientWithProxy(30*time.Second, proxy),
	}
}

// NewClient builds a client from the current global config.
func NewClient() *Client {
	return NewClientFrom(config.Snapshot())
}

func (c *Client) SetDebug(d bool) { c.Debug = d }

func (c *Client) model() string {
	if m := strings.TrimSpace(c.cfg.Model); m != "" {
		return m
	}
	return DefaultModel
}

func (c *Client) endpoint() string {
	if e := strings.TrimSpace(c.cfg.Endpoint); e != "" {
		return strings.TrimRight(e, "/")
	}
	return DefaultEndpoint
}

func (c *Client) cliVersion() string {
	if v := strings.TrimSpace(c.cfg.CLIVersion); v != "" {
		return v
	}
	return defaultCLIVersion
}

// setHeaders mirrors the official CLI's wire traffic exactly (v1.56.0).
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.cfg.APIKey))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("X-Cli-Environment", "production")
	req.Header.Set("X-Command-Code-Version", c.cliVersion())
}

// GetCredits fetches the billing/credits payload — the single request behind
// both `glm status` and the daemon's quota queries.
func (c *Client) GetCredits() (*Credits, error) {
	body, status, err := c.get(CreditsPath)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("credits: %d (%s)", status, errorHint(body, status))
	}
	return parseCredits(body)
}

func parseCredits(body []byte) (*Credits, error) {
	var doc struct {
		Credits struct {
			Monthly float64 `json:"monthlyCredits"`
		} `json:"credits"`
		WindowLimits struct {
			Five   wireWindow `json:"fiveHour"`
			Weekly wireWindow `json:"weekly"`
		} `json:"windowLimits"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse credits: %w (body: %.200s)", err, body)
	}
	return &Credits{
		Monthly: doc.Credits.Monthly,
		Five:    doc.WindowLimits.Five.window(),
		Weekly:  doc.WindowLimits.Weekly.window(),
	}, nil
}

// capCache persists the computed monthly cap across CLI invocations
// (~/.cache/glm/cc.json, 0600). The allotment cannot change while the same
// key sits inside the same billing period, so the cache is keyed by the API
// key (sha256 — the key itself is never written) and valid until the
// subscription period ends; a changed key or a lapsed period forces a
// recompute. Remaining > cap is impossible while the cap is right, so it
// also forces one (plan upgraded mid-period).
type capCache struct {
	KeyHash   string    `json:"key_hash,omitempty"`
	Cap       float64   `json:"cap,omitempty"`
	PeriodEnd time.Time `json:"period_end,omitempty"`
}

func capCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "glm", "cc.json"), nil
}

func apiKeyHash(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

func loadCapCache() capCache {
	var dc capCache
	path, err := capCachePath()
	if err != nil {
		return dc
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &dc) != nil {
		return capCache{}
	}
	return dc
}

func saveCapCache(dc capCache) {
	path, err := capCachePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	data, err := json.Marshal(dc)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// MonthlyCap returns the plan's monthly credit allotment. The credits
// endpoint reports only the remainder, so the cap is that remainder plus
// what the usage summary says was spent since the subscription period began
// (remaining + spent = allotment; aborted requests can burn credits without
// reaching the summary ledger, so the sum can sit a hair under the true
// plan value — display-only, and it recomputes next period). Cached until
// the period ends or the key changes — a warm `glm status` is a single
// credits request.
func (c *Client) MonthlyCap(remaining float64) (float64, error) {
	hash := apiKeyHash(c.cfg.APIKey)
	if cached := loadCapCache(); cached.Cap > 0 && cached.Cap >= remaining &&
		cached.KeyHash == hash && cached.PeriodEnd.After(time.Now()) {
		log.Debugf("Monthly cap %.2f from cache (period ends %s)",
			cached.Cap, cached.PeriodEnd.Local().Format("2006-01-02 15:04:05"))
		return cached.Cap, nil
	}

	start, periodEnd, err := c.periodBounds()
	if err != nil {
		return 0, err
	}
	data, status, err := c.get(UsageSummaryPath + "?since=" + url.QueryEscape(start))
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("usage summary: %d (body: %.200s)", status, data)
	}
	var sum struct {
		TotalCredits float64 `json:"totalCredits"`
	}
	if err := json.Unmarshal(data, &sum); err != nil {
		return 0, fmt.Errorf("parse usage summary: %w (body: %.200s)", err, data)
	}
	cap := remaining + sum.TotalCredits
	if cap <= 0 {
		return 0, fmt.Errorf("computed monthly cap is %.2f — no active usage this period?", cap)
	}
	if !periodEnd.IsZero() {
		saveCapCache(capCache{KeyHash: hash, Cap: cap, PeriodEnd: periodEnd})
	}
	return cap, nil
}

// periodBounds fetches the active subscription's current billing period as
// an RFC3339 start (the `since` anchor the usage summary needs) plus its end
// (the cache validity bound).
func (c *Client) periodBounds() (string, time.Time, error) {
	data, status, err := c.get(SubscriptionsPath)
	if err != nil {
		return "", time.Time{}, err
	}
	if status != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("subscriptions: %d (body: %.200s)", status, data)
	}
	var sub struct {
		Success bool `json:"success"`
		Data    struct {
			Status             string `json:"status"`
			CurrentPeriodStart string `json:"currentPeriodStart"`
			CurrentPeriodEnd   string `json:"currentPeriodEnd"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &sub); err != nil {
		return "", time.Time{}, fmt.Errorf("parse subscriptions: %w (body: %.200s)", err, data)
	}
	if !sub.Success || sub.Data.CurrentPeriodStart == "" {
		return "", time.Time{}, fmt.Errorf("no active subscription in response (body: %.200s)", data)
	}
	start, err := time.Parse(time.RFC3339, sub.Data.CurrentPeriodStart)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse currentPeriodStart %q: %w", sub.Data.CurrentPeriodStart, err)
	}
	var end time.Time
	if sub.Data.CurrentPeriodEnd != "" {
		if t, err := time.Parse(time.RFC3339, sub.Data.CurrentPeriodEnd); err == nil {
			end = t
		}
	}
	return start.UTC().Format(time.RFC3339), end, nil
}

// get issues a GET with the CLI headers and returns body + status.
func (c *Client) get(path string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", c.endpoint()+path, nil)
	if err != nil {
		return nil, 0, err
	}
	c.setHeaders(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

type wireWindow struct {
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	Exceeded bool    `json:"exceeded"`
	ResetAt  int64   `json:"resetAt"`
}

func (w wireWindow) window() Window {
	win := Window{Used: w.Used, Cap: w.Cap, Exceeded: w.Exceeded}
	if w.ResetAt > 0 {
		win.ResetAt = time.UnixMilli(w.ResetAt)
	}
	return win
}

// Quota maps the 5h window onto glm.QuotaStatus so the shared daemon state
// machine can drive it: Remaining is 0-100 (rounded), 100 means the window
// is fresh, <=0 exhausted.
//
// The bucket is rolling: once ResetAt has passed the window is over and the
// next request starts a fresh one, so a past reset reads as fresh even while
// the API still displays the last cycle's used total.
func (cr *Credits) Quota() *glm.QuotaStatus {
	w := cr.Five
	usedPct := 0.0
	if w.Cap > 0 {
		usedPct = w.Used / w.Cap * 100
	}
	if !w.ResetAt.IsZero() && !w.ResetAt.After(time.Now()) {
		usedPct = 0
	}
	remaining := 100 - int64(usedPct+0.5)
	if remaining < 0 {
		remaining = 0
	}
	return &glm.QuotaStatus{
		Used:      100 - remaining,
		Limit:     100,
		Remaining: remaining,
		ResetTime: w.ResetAt,
		Found:     true,
	}
}

// GetQuota maps the configured account's 5h window for the daemon state
// machine (see Credits.Quota).
func (c *Client) GetQuota() (*glm.QuotaStatus, error) {
	creds, err := c.GetCredits()
	if err != nil {
		return nil, err
	}
	if creds.Five.Cap <= 0 {
		return nil, fmt.Errorf("no 5h window reported (cap=0)")
	}
	q := creds.Quota()
	q.Raw = fmt.Sprintf("5h used %.6g of cap %g", creds.Five.Used, creds.Five.Cap)
	return q, nil
}

// SendHeartbeat fires the minimal warmup request. A request inside a live
// window is absorbed by it (~1.4e-5 credits); only a request after the
// window's reset anchors a new one.
func (c *Client) SendHeartbeat() error {
	payload, _ := json.Marshal(map[string]interface{}{
		"model":             c.model(),
		"input":             "1",
		"max_output_tokens": 16,
	})
	req, err := http.NewRequest("POST", c.endpoint()+ResponsesPath, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.setHeaders(req)

	log.Debugf("POST %s%s (%s)", c.endpoint(), ResponsesPath, c.model())
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("warmup failed: %d (%s)", resp.StatusCode, errorHint(body, resp.StatusCode))
	}
	log.Debugf("Warmup ok (%s)", c.model())
	return nil
}

// errorHint extracts the server's error message; the API uses two shapes:
// {"error":{"message":...}} (responses) and {"error":{"message":...,
// "code":"UNAUTHORIZED"}} wrapped in success:false (billing).
func errorHint(body []byte, status int) string {
	var er struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &er) == nil && er.Error.Message != "" {
		if er.Error.Code != "" && er.Error.Code != "FORBIDDEN" {
			return er.Error.Code + ": " + er.Error.Message
		}
		return er.Error.Message
	}
	if status == http.StatusForbidden {
		return "forbidden — model not in plan?"
	}
	return ""
}

// Activate mirrors glm.Client.Activate: skip when the window is live and not
// forced, sleep out an imminent reset in service mode, send the warmup, then
// verify the anchor by polling the credits endpoint. A window counts as
// anchored once its reset moved into the future — on a never-started account
// the tiny warmup never moves the used percentage, so that is the signal the
// verify loop watches for.
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

	before := time.Now()
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
		if q.Remaining < 100 || q.ResetTime.After(before) {
			quota = q
			verified = true
			break
		}
		log.Debugf("Verify attempt %d: window not anchored yet, retrying...", i+1)
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

// FormatTimeUntil is re-exported for command printers.
func FormatTimeUntil(t time.Time) string { return glm.FormatTimeUntil(t) }

package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

const (
	// CodexBase is where Codex CLI sends a turn. Usage lives one segment up,
	// at /backend-api/wham/usage (magpie CodexBase + subscription_usage.go).
	CodexBase = "https://chatgpt.com/backend-api/codex"
	// TokenURL is Codex CLI's OAuth refresh endpoint.
	TokenURL = "https://auth.openai.com/oauth/token"
	// ClientID is Codex CLI's installed-app OAuth client (public by design;
	// the CLI ships it).
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

	// fallbackCLIVersion is used only when neither config nor the local
	// models cache names a Codex CLI. The backend 404s a model its own
	// /models lists when the User-Agent is not a current codex_cli_rs.
	fallbackCLIVersion = "0.163.0"

	pingCacheKey = "glm-codex-5h-ping"
)

// Client talks to the ChatGPT Codex backend with the local Codex CLI login.
type Client struct {
	cfg    config.CodexConfig
	client *http.Client
	Debug  bool

	mu sync.Mutex
}

// NewClientFrom builds a client from a config snapshot.
func NewClientFrom(cfg config.Config) *Client {
	proxy := cfg.Codex.Proxy
	if proxy == "" {
		proxy = cfg.Proxy
	}
	return &Client{
		cfg:    cfg.Codex,
		client: httputil.NewHttpClientWithProxy(2*time.Minute, proxy),
	}
}

// NewClient builds a client from the current global config.
func NewClient() *Client { return NewClientFrom(config.Snapshot()) }

func (c *Client) SetDebug(d bool) { c.Debug = d }

func (c *Client) model() string {
	if m := strings.TrimSpace(c.cfg.Model); m != "" {
		return m
	}
	return DefaultModel
}

func (c *Client) effort() string {
	if e := strings.TrimSpace(c.cfg.Effort); e != "" {
		return e
	}
	return DefaultEffort
}

// Home is the Codex CLI state directory: config, else CODEX_HOME, else ~/.codex.
func Home(cfg config.CodexConfig) string {
	if h := strings.TrimSpace(cfg.Home); h != "" {
		return h
	}
	if h := strings.TrimSpace(os.Getenv("CODEX_HOME")); h != "" {
		return h
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(dir, ".codex")
}

func (c *Client) home() string { return Home(c.cfg) }

func (c *Client) authPath() string { return filepath.Join(c.home(), "auth.json") }

// Usage fetches the account's rate-limit windows.
func (c *Client) Usage() (Usage, error) {
	base := strings.TrimSuffix(strings.TrimRight(c.base(), "/"), "/codex")
	body, status, err := c.get(base + "/wham/usage")
	if err != nil {
		return Usage{}, err
	}
	if status != http.StatusOK {
		return Usage{}, fmt.Errorf("usage: %d (body: %.200s)", status, body)
	}
	return ParseUsage(body)
}

// GetQuota maps the 5h window for the shared provider contract.
func (c *Client) GetQuota() (*glm.QuotaStatus, error) {
	u, err := c.Usage()
	if err != nil {
		return nil, err
	}
	return u.Quota(time.Now()), nil
}

// Ping sends one gpt-6-luna "hello", the way Codex CLI sends a turn.
func (c *Client) Ping() error {
	body, err := pingBody(c.model(), c.effort(), c.instructions())
	if err != nil {
		return err
	}
	raw, status, err := c.post(strings.TrimRight(c.base(), "/")+"/responses", body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("ping: %d (body: %.200s)", status, raw)
	}
	return nil
}

// Activate pings only when the 5h window is not open, unless force.
// A live window absorbs the request and must not be re-anchored early.
func (c *Client) Activate(force bool, _ bool) (*glm.QuotaStatus, error) {
	u, err := c.Usage()
	if err != nil {
		return nil, fmt.Errorf("get quota: %w", err)
	}
	now := time.Now()
	if !force && u.Five.Open(now) {
		return u.Quota(now), nil
	}
	if err := c.Ping(); err != nil {
		return u.Quota(now), err
	}
	if again, err := c.Usage(); err == nil {
		u = again
	}
	return u.Quota(time.Now()), nil
}

func (c *Client) base() string {
	if e := strings.TrimSpace(c.cfg.Endpoint); e != "" {
		return strings.TrimRight(e, "/")
	}
	return CodexBase
}

func (c *Client) get(url string) ([]byte, int, error) {
	return c.do(http.MethodGet, url, nil)
}

func (c *Client) post(url string, body []byte) ([]byte, int, error) {
	return c.do(http.MethodPost, url, body)
}

func (c *Client) do(method, url string, body []byte) ([]byte, int, error) {
	tok, account, err := c.token(context.Background())
	if err != nil {
		return nil, 0, err
	}
	raw, status, err := c.round(method, url, body, tok, account)
	if err != nil {
		return nil, 0, err
	}
	if status == http.StatusUnauthorized {
		tok, account, err = c.forceRefresh(context.Background())
		if err != nil {
			return raw, status, err
		}
		return c.round(method, url, body, tok, account)
	}
	return raw, status, nil
}

func (c *Client) round(method, url string, body []byte, tok, account string) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if account != "" {
		req.Header.Set("chatgpt-account-id", account)
	}
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	ver := c.cliVersion()
	req.Header.Set("version", ver)
	req.Header.Set("User-Agent", userAgent(ver))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("session-id", pingCacheKey)
		req.Header.Set("thread-id", pingCacheKey)
	} else {
		req.Header.Set("Accept", "application/json")
	}
	log.Debugf("%s %s", method, url)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if c.Debug {
		log.Debugf("status %d body %.400s", resp.StatusCode, raw)
	}
	return raw, resp.StatusCode, nil
}

func pingBody(model, effort, instructions string) ([]byte, error) {
	m := map[string]any{
		"model": model,
		"input": []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": PingText,
			}},
		}},
		"store":               false,
		"stream":              true,
		"tool_choice":         "none",
		"parallel_tool_calls": false,
		"prompt_cache_key":    pingCacheKey,
		"text":                map[string]any{"verbosity": "low"},
	}
	if instructions != "" {
		m["instructions"] = instructions
	}
	if effort != "" && effort != "none" {
		m["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
		m["include"] = []any{"reasoning.encrypted_content"}
	}
	return json.Marshal(m)
}

// instructions is the model's Codex CLI prompt from models_cache.json, when
// the CLI has fetched one. The backend expects that prompt on a Codex turn.
func (c *Client) instructions() string {
	b, err := os.ReadFile(filepath.Join(c.home(), "models_cache.json"))
	if err != nil {
		return ""
	}
	var cache struct {
		Models []struct {
			Slug     string `json:"slug"`
			Messages struct {
				Template string `json:"instructions_template"`
			} `json:"model_messages"`
		} `json:"models"`
	}
	if json.Unmarshal(b, &cache) != nil {
		return ""
	}
	want := c.model()
	for _, m := range cache.Models {
		if m.Slug == want && m.Messages.Template != "" {
			return m.Messages.Template
		}
	}
	return ""
}

func (c *Client) cliVersion() string {
	if v := strings.TrimSpace(c.cfg.CLIVersion); v != "" {
		return v
	}
	b, err := os.ReadFile(filepath.Join(c.home(), "models_cache.json"))
	if err == nil {
		var cache struct {
			Version string `json:"client_version"`
		}
		if json.Unmarshal(b, &cache) == nil && cache.Version != "" {
			return cache.Version
		}
	}
	return fallbackCLIVersion
}

func userAgent(version string) string {
	arch := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	return "codex_cli_rs/" + version + " (" + osName() + "; " + arch + ") xterm-256color"
}

func osName() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return "Linux"
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "Linux"
	}
	// Codex CLI sends a three-part version ("Linux 6.8.0").
	if i := strings.IndexByte(v, '-'); i > 0 && strings.Count(v[:i], ".") >= 2 {
		v = v[:i]
	}
	if strings.Count(v, ".") < 2 {
		v += ".0"
	}
	return "Linux " + v
}

type authFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

func (c *Client) token(ctx context.Context) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, a, err := c.readAuth()
	if err != nil {
		return "", "", err
	}
	if a.AuthMode == "apikey" || a.Tokens.AccessToken == "" {
		return "", "", fmt.Errorf("Codex CLI is not signed in with ChatGPT; run codex login")
	}
	if !expiring(a.Tokens.AccessToken, time.Now()) {
		return a.Tokens.AccessToken, accountID(a), nil
	}
	tok, err := c.refresh(ctx, raw)
	if err != nil {
		return "", "", err
	}
	if err := writeAuth(c.authPath(), raw); err != nil {
		log.Errorf("codex: refreshed sign-in was not written to %s: %v", c.authPath(), err)
	}
	return tok, accountID(a), nil
}

func (c *Client) forceRefresh(ctx context.Context) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, a, err := c.readAuth()
	if err != nil {
		return "", "", err
	}
	tok, err := c.refresh(ctx, raw)
	if err != nil {
		return "", "", err
	}
	if err := writeAuth(c.authPath(), raw); err != nil {
		log.Errorf("codex: refreshed sign-in was not written to %s: %v", c.authPath(), err)
	}
	return tok, accountID(a), nil
}

func (c *Client) readAuth() (map[string]any, authFile, error) {
	b, err := os.ReadFile(c.authPath())
	if err != nil {
		return nil, authFile{}, fmt.Errorf("Codex CLI auth not found at %s; run codex login", c.authPath())
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, authFile{}, fmt.Errorf("parse %s: %w", c.authPath(), err)
	}
	var a authFile
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, authFile{}, fmt.Errorf("parse %s: %w", c.authPath(), err)
	}
	return raw, a, nil
}

func accountID(a authFile) string {
	if a.Tokens.AccountID != "" {
		return a.Tokens.AccountID
	}
	claims := jwtClaims(a.Tokens.IDToken)
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	id, _ := auth["chatgpt_account_id"].(string)
	return id
}

// PlanOf reports the ChatGPT plan named in the id token, if any.
func PlanOf(idToken string) string {
	auth, _ := jwtClaims(idToken)["https://api.openai.com/auth"].(map[string]any)
	plan, _ := auth["chatgpt_plan_type"].(string)
	return plan
}

func expiring(access string, now time.Time) bool {
	exp, _ := jwtClaims(access)["exp"].(float64)
	if exp == 0 {
		return false
	}
	return time.Unix(int64(exp), 0).Sub(now) <= 5*time.Minute
}

func jwtClaims(tok string) map[string]any {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func (c *Client) refresh(ctx context.Context, raw map[string]any) (string, error) {
	toks, _ := raw["tokens"].(map[string]any)
	if toks == nil {
		return "", fmt.Errorf("Codex CLI auth has no tokens; run codex login")
	}
	refreshTok, _ := toks["refresh_token"].(string)
	if refreshTok == "" {
		return "", fmt.Errorf("Codex CLI auth has no refresh token; run codex login")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Detach from the caller's cancel: OpenAI rotates the refresh token as
	// it answers, and dropping the response leaves the spent token on disk.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]string{
		"client_id":     ClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshTok,
		"scope":         "openid profile email",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("codex token refresh: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var fresh struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(b, &fresh) != nil || fresh.AccessToken == "" {
		return "", fmt.Errorf("codex token refresh failed (%d); run codex login", resp.StatusCode)
	}
	toks["access_token"] = fresh.AccessToken
	if fresh.IDToken != "" {
		toks["id_token"] = fresh.IDToken
	}
	if fresh.RefreshToken != "" {
		toks["refresh_token"] = fresh.RefreshToken
	}
	raw["tokens"] = toks
	raw["last_refresh"] = time.Now().UTC().Format(time.RFC3339Nano)
	return fresh.AccessToken, nil
}

func writeAuth(path string, raw map[string]any) error {
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoggedIn reports whether auth.json holds a ChatGPT access token.
func LoggedIn(cfg config.CodexConfig) (plan string, err error) {
	b, err := os.ReadFile(filepath.Join(Home(cfg), "auth.json"))
	if err != nil {
		return "", fmt.Errorf("Codex CLI auth not found; run codex login")
	}
	var a authFile
	if json.Unmarshal(b, &a) != nil || a.Tokens.AccessToken == "" || a.AuthMode == "apikey" {
		return "", fmt.Errorf("Codex CLI is not signed in with ChatGPT; run codex login")
	}
	return PlanOf(a.Tokens.IDToken), nil
}

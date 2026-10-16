package codex

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xihale/glm/pkg/config"
)

func TestWindowOpen(t *testing.T) {
	now := time.Date(2026, 10, 16, 16, 0, 0, 0, time.Local)
	span := 5 * time.Hour
	cases := []struct {
		name string
		w    Window
		open bool
	}{
		{"live", Window{Span: span, Reset: now.Add(2 * time.Hour), UsedPercent: 40}, true},
		{"just started, usage not counted", Window{Span: span, Reset: now.Add(span - 20*time.Minute)}, true},
		{"idle placeholder", Window{Span: span, Reset: now.Add(span - time.Minute)}, false},
		{"reset passed", Window{Span: span, Reset: now.Add(-time.Minute), UsedPercent: 10}, false},
		{"no reset", Window{Span: span}, false},
		{"unknown span with a future reset", Window{Reset: now.Add(time.Hour)}, true},
	}
	for _, tc := range cases {
		if got := tc.w.Open(now); got != tc.open {
			t.Errorf("%s: Open=%v want %v", tc.name, got, tc.open)
		}
	}
}

func TestPlanCheck(t *testing.T) {
	now := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	live := Window{Span: 5 * time.Hour, Reset: reset}
	p := PlanCheck(live, now)
	if p.Action != ActionWait || !p.Next.Equal(reset) {
		t.Fatalf("open window: %+v", p)
	}
	idle := Window{Span: 5 * time.Hour, Reset: now.Add(5*time.Hour - time.Minute)}
	p = PlanCheck(idle, now)
	if p.Action != ActionPing {
		t.Fatalf("idle window: %+v", p)
	}
	if next := NextAfterPing(live, now); !next.Equal(reset) {
		t.Fatalf("after ping live: %v", next)
	}
	if next := NextAfterPing(idle, now); !next.Equal(now.Add(PostPingWait)) {
		t.Fatalf("after ping still idle: %v", next)
	}
}

// TestPingOncePerWindow pins the one-hello contract: right after a ping the
// usage read still shows the idle placeholder (0% used, reset a full span
// ahead), and the next wake must land past WarmSlack so the placeholder
// classifies as open instead of triggering a second hello.
func TestPingOncePerWindow(t *testing.T) {
	now := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)
	span := 5 * time.Hour
	fresh := Window{Span: span, Reset: now.Add(span)}
	next := NextAfterPing(fresh, now)
	if next.Equal(now.Add(IdleRecheck)) {
		t.Fatalf("post-ping placeholder must not recheck at IdleRecheck: %v", next)
	}
	if !fresh.Open(next) {
		t.Fatalf("window not open at post-ping wake %v (delta %v, slack %v)",
			next, fresh.Reset.Sub(next), WarmSlack)
	}
	if p := PlanCheck(fresh, next); p.Action != ActionWait {
		t.Fatalf("post-ping check re-pings: %+v", p)
	}
}

func TestSleptThrough(t *testing.T) {
	if SleptThrough(30*time.Second, 30*time.Second) {
		t.Fatal("an ordinary chunk is not a resume")
	}
	if !SleptThrough(2*time.Hour, 30*time.Second) {
		t.Fatal("wall clock running ahead of the timer is a resume")
	}
}

func TestParseUsage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	five := map[string]any{
		"used_percent":         12.5,
		"limit_window_seconds": 18000,
		"reset_at":             now.Add(3 * time.Hour).Unix(),
	}
	week := map[string]any{
		"used_percent":         30,
		"limit_window_seconds": 604800,
		"reset_at":             now.Add(6 * 24 * time.Hour).Unix(),
	}
	raw, _ := json.Marshal(map[string]any{
		"plan_type": "plus",
		"rate_limit": map[string]any{
			"primary_window":   five,
			"secondary_window": week,
		},
	})
	u, err := ParseUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Plan != "plus" || u.Five.Name != "5 hours" || u.Week.Name != "7 days" {
		t.Fatalf("parsed %+v week %+v", u.Five, u.Week)
	}
	if !u.Five.Open(now) {
		t.Fatal("3h left on a 5h window should be open")
	}
	q := u.Quota(now)
	if q.Remaining != 88 || q.ResetTime.IsZero() {
		t.Fatalf("quota %+v", q)
	}

	raw, _ = json.Marshal(map[string]any{
		"plan_type": "plus",
		"rate_limit": map[string]any{
			"primary_window":   week,
			"secondary_window": five,
		},
	})
	u, err = ParseUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Five.Span != 5*time.Hour || u.Week.Span != 7*24*time.Hour {
		t.Fatalf("span pick failed: five %v week %v", u.Five.Span, u.Week.Span)
	}
}

func TestIdleQuotaHidesPlaceholderReset(t *testing.T) {
	now := time.Now()
	u := Usage{Five: Window{Span: 5 * time.Hour, Reset: now.Add(5*time.Hour - time.Minute)}}
	q := u.Quota(now)
	if q.Remaining != 100 || !q.ResetTime.IsZero() {
		t.Fatalf("idle quota should not look like a live countdown: %+v", q)
	}
}

func TestPingRequest(t *testing.T) {
	var got struct {
		method string
		body   map[string]any
		ua     string
		acct   string
		beta   string
		origin string
		sess   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.ua = r.Header.Get("User-Agent")
		got.acct = r.Header.Get("chatgpt-account-id")
		got.beta = r.Header.Get("OpenAI-Beta")
		got.origin = r.Header.Get("originator")
		got.sess = r.Header.Get("session-id")
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Errorf("path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: ok\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	auth := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  jwtWithExp(time.Now().Add(time.Hour)),
			"refresh_token": "rt",
			"account_id":    "acct-1",
		},
	}
	raw, _ := json.MarshalIndent(auth, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	c := NewClientFrom(config.Config{Codex: config.CodexConfig{
		Home: dir, Endpoint: srv.URL + "/backend-api/codex", Model: "gpt-6-luna",
	}})
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.acct != "acct-1" || got.beta != "responses=experimental" || got.origin != "codex_cli_rs" || got.sess == "" {
		t.Fatalf("headers: %+v", got)
	}
	if !strings.HasPrefix(got.ua, "codex_cli_rs/") {
		t.Fatalf("user-agent %q", got.ua)
	}
	if got.body["model"] != "gpt-6-luna" || got.body["store"] != false || got.body["stream"] != true || got.body["tool_choice"] != "none" {
		t.Fatalf("body %#v", got.body)
	}
	in, _ := got.body["input"].([]any)
	if len(in) != 1 {
		t.Fatalf("input %#v", got.body["input"])
	}
	msg, _ := in[0].(map[string]any)
	content, _ := msg["content"].([]any)
	part, _ := content[0].(map[string]any)
	if part["text"] != "hello" {
		t.Fatalf("text %#v", part)
	}
}

func jwtWithExp(exp time.Time) string {
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix()})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

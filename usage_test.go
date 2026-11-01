package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The replies below are shaped like ones measured from the live endpoint on 2026-10-31 (fields
// spillover does not read are trimmed).
const (
	proUsage = `{"five_hour": {"utilization": 12.0, "resets_at": "2026-11-01T00:20:00.027961+00:00"},
		"seven_day": {"utilization": 2.0, "resets_at": "2026-11-04T10:00:00.027981+00:00"},
		"seven_day_opus": null, "seven_day_sonnet": null,
		"limits": [{"kind": "session", "group": "session", "percent": 12, "resets_at": "2026-11-01T00:20:00.027961+00:00", "scope": null},
		           {"kind": "weekly_all", "group": "weekly", "percent": 2, "resets_at": "2026-11-04T10:00:00.027981+00:00", "scope": null}]}`
	maxUsage = `{"five_hour": null,
		"seven_day": {"utilization": 89.0, "resets_at": "2026-11-03T05:59:59.967494+00:00"},
		"limits": [{"kind": "weekly_all", "group": "weekly", "percent": 89, "resets_at": "2026-11-03T05:59:59.967494+00:00", "scope": null},
		           {"kind": "weekly_scoped", "group": "weekly", "percent": 0, "resets_at": "2026-11-03T05:59:59.967677+00:00",
		            "scope": {"model": {"id": null, "display_name": "Fable"}, "surface": null}}]}`
)

// usageServer stands in for the usage endpoint: it answers each bearer with its own reply and
// records who asked for what.
type usageServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newUsageServer(t *testing.T, replies map[string]func(w http.ResponseWriter, r *http.Request)) *usageServer {
	u := &usageServer{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.seen = append(u.seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		u.mu.Unlock()
		reply, ok := replies[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok || r.URL.Path != usagePath {
			w.WriteHeader(404)
			return
		}
		reply(w, r)
	}))
	t.Cleanup(u.Close)
	return u
}

// asked is every request so far, as "METHOD path Authorization".
func (u *usageServer) asked() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.seen...)
}

func answer(body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

var usageNow = time.Date(2026, 10, 31, 21, 33, 0, 0, time.UTC)

func usageAccounts(t *testing.T, logins map[string]string) *accounts {
	a := newAccounts(makeHome(t, logins), credentialsFile, "")
	a.claude = ""
	a.now = func() time.Time { return usageNow }
	return a
}

func TestUsageAsksEveryAccountWithItsOwnToken(t *testing.T) {
	up := newUsageServer(t, map[string]func(http.ResponseWriter, *http.Request){"tok-default": answer(maxUsage), "tok-work": answer(proUsage)})
	a := usageAccounts(t, map[string]string{"default": "tok-default", "work": "tok-work"})
	var out bytes.Buffer
	if err := showUsage(a, up.URL, usageQuery{all: true, json: true, proxyChoice: "work", proxyUp: true}, &out); err != nil {
		t.Fatal(err)
	}
	asked := up.asked()
	sort.Strings(asked)
	if strings.Join(asked, ",") != "GET /api/oauth/usage Bearer tok-default,GET /api/oauth/usage Bearer tok-work" {
		t.Fatal(asked)
	}
	var rows []accountUsage
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err, out.String())
	}
	if len(rows) != 2 || rows[0].ID != "default" || rows[0].Active || rows[1].ID != "work" || !rows[1].Active {
		t.Fatal(out.String())
	}
	maxPlan, proPlan := rows[0], rows[1]
	if maxPlan.FiveHour != nil || maxPlan.Weekly.Percent != 89 || !maxPlan.Weekly.ResetsAt.Equal(time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)) {
		t.Fatal("max account", out.String())
	}
	if len(maxPlan.Models) != 1 || maxPlan.Models[0].Model != "Fable" || maxPlan.Models[0].Percent != 0 {
		t.Fatal("weekly by model", out.String())
	}
	if proPlan.FiveHour.Percent != 12 || !proPlan.FiveHour.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 20, 0, 0, time.UTC)) || proPlan.Weekly.Percent != 2 || len(proPlan.Models) != 0 {
		t.Fatal("pro account", out.String())
	}
}

func TestUsageTableShowsEveryAccountAndMarksTheActiveOne(t *testing.T) {
	up := newUsageServer(t, map[string]func(http.ResponseWriter, *http.Request){"tok-default": answer(maxUsage), "tok-work": answer(proUsage)})
	a := usageAccounts(t, map[string]string{"default": "tok-default", "work": "tok-work"})
	var out bytes.Buffer
	if err := showUsage(a, up.URL, usageQuery{all: true, proxyChoice: "work", proxyUp: true}, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatal(out.String())
	}
	if f := strings.Fields(lines[1]); strings.Join(f, " ") != "default default@example.com - - 89% Tue 06:00 (2d8h) Fable 0%" {
		t.Fatal(lines[1])
	}
	if f := strings.Fields(lines[2]); strings.Join(f, " ") != "* work work@example.com 12% Sun 00:20 (2h47m) 2% Wed 10:00 (3d12h) -" {
		t.Fatal(lines[2])
	}
}

func TestUsageShowsTheActiveAccountAndTheDefaultWhenTheProxyIsStopped(t *testing.T) {
	up := newUsageServer(t, map[string]func(http.ResponseWriter, *http.Request){"tok-default": answer(maxUsage), "tok-work": answer(proUsage)})
	a := usageAccounts(t, map[string]string{"default": "tok-default", "work": "tok-work"})
	var out bytes.Buffer
	if err := showUsage(a, up.URL, usageQuery{proxyChoice: "work", proxyUp: true}, &out); err != nil {
		t.Fatal(err)
	}
	if want := "work (work@example.com)\n  5-hour  12%  Sun 00:20 (2h47m)\n  weekly  2%   Wed 10:00 (3d12h)\n"; out.String() != want {
		t.Fatalf("%q", out.String())
	}
	out.Reset()
	if err := showUsage(a, up.URL, usageQuery{}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "default (default@example.com)\n") || !strings.Contains(out.String(), "weekly Fable  0%") ||
		!strings.HasSuffix(out.String(), proxyDownNote+"\n") {
		t.Fatal(out.String())
	}
	if asked := up.asked(); len(asked) != 2 || !strings.HasSuffix(asked[0], "Bearer tok-work") || !strings.HasSuffix(asked[1], "Bearer tok-default") {
		t.Fatal("only the shown account is asked", asked)
	}
}

func TestUsageNeverShowsATokenEvenWhenTheServerEchoesIt(t *testing.T) {
	echo := func(status int) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]string{
				"type": "authentication_error", "message": "bad credentials: " + r.Header.Get("Authorization")}})
		}
	}
	up := newUsageServer(t, map[string]func(http.ResponseWriter, *http.Request){"tok-default-secret": echo(401), "tok-work-secret": echo(500)})
	a := usageAccounts(t, map[string]string{"default": "tok-default-secret", "work": "tok-work-secret"})
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	var shown []string
	for _, q := range []usageQuery{{all: true}, {all: true, json: true}, {}, {json: true}} {
		var out bytes.Buffer
		err := showUsage(a, up.URL, q, &out)
		if err == nil {
			t.Fatal("a failed read reported as read", q)
		}
		shown = append(shown, out.String(), err.Error())
	}
	all := strings.Join(shown, "\n") + logged.String()
	if strings.Contains(all, "tok-") || strings.Contains(all, "secret") {
		t.Fatal(all)
	}
	if !strings.Contains(all, "default: HTTP 401 authentication_error: bad credentials: Bearer [token]") || !strings.Contains(all, "usage not read for 2 of 2 accounts") {
		t.Fatal("the reason is lost", all)
	}
}

func TestUsageFlagsAndShortDurations(t *testing.T) {
	if q, ok := usageFlags([]string{"--all", "--json"}); !ok || !q.all || !q.json {
		t.Fatal(q, ok)
	}
	if _, ok := usageFlags([]string{"work"}); ok {
		t.Fatal("an account name is not a flag")
	}
	cases := map[time.Duration]string{
		-time.Minute:                      "now",
		12 * time.Minute:                  "12m",
		2*time.Hour + 47*time.Minute:      "2h47m",
		3*24*time.Hour + 12*time.Hour + 5: "3d12h",
	}
	for d, want := range cases {
		if got := shortDuration(d); got != want {
			t.Fatal(d, got)
		}
	}
	if got := percentText(&usageWindow{Percent: 12.34}); got != "12.3%" {
		t.Fatal(got)
	}
}

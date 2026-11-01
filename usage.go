package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"
)

// usagePath is where Claude Code's own `/usage` reads how full the bearer's 5-hour and weekly
// windows are.
const usagePath = "/api/oauth/usage"

// usageWindow is one usage window of an account: how full it is (percent of its limit) and when it
// resets. Model is set on a weekly window that counts a single model.
type usageWindow struct {
	Model    string     `json:"model,omitempty"`
	Percent  float64    `json:"percent"`
	ResetsAt *time.Time `json:"resetsAt"`
}

// accountUsage is one account's row in `spillover usage`. A nil window is one the reply did not
// carry.
type accountUsage struct {
	ID       string        `json:"id"`
	Email    string        `json:"email,omitempty"`
	Active   bool          `json:"active"`
	FiveHour *usageWindow  `json:"fiveHour"`
	Weekly   *usageWindow  `json:"weekly"`
	Models   []usageWindow `json:"models"`
	Error    string        `json:"error,omitempty"`
}

// usageReply is the part of the usage endpoint's answer spillover reads.
type usageReply struct {
	FiveHour *replyWindow `json:"five_hour"`
	SevenDay *replyWindow `json:"seven_day"`
	Limits   []struct {
		Kind     string   `json:"kind"`
		Percent  *float64 `json:"percent"`
		ResetsAt string   `json:"resets_at"`
		Scope    *struct {
			Model *struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	} `json:"limits"`
}

type replyWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

func (r *replyWindow) window() *usageWindow {
	if r == nil {
		return nil
	}
	return newWindow(r.Utilization, r.ResetsAt)
}

// newWindow is nil when the percent is missing; a reset time that does not parse is left unknown.
func newWindow(percent *float64, resetsAt string) *usageWindow {
	if percent == nil {
		return nil
	}
	w := &usageWindow{Percent: *percent}
	if t, err := time.Parse(time.RFC3339, resetsAt); err == nil {
		// The endpoint stamps a reset up to a second off its minute.
		t = t.Round(time.Minute).UTC()
		w.ResetsAt = &t
	}
	return w
}

// readUsage asks the usage endpoint at base how full the token's account is. Nothing it returns
// carries the token, even when the server echoes it.
func readUsage(client *http.Client, base, token string) (accountUsage, error) {
	hide := func(s string) string {
		if token == "" {
			return s
		}
		return strings.ReplaceAll(s, token, "[token]")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+usagePath, nil)
	if err != nil {
		return accountUsage{}, fmt.Errorf("bad API address: %s", hide(err.Error()))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "spillover")
	resp, err := client.Do(req)
	if err != nil {
		return accountUsage{}, fmt.Errorf("usage endpoint unreachable: %s", hide(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var reply struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &reply)
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if reply.Error.Type != "" {
			msg += " " + reply.Error.Type
		}
		if m := reply.Error.Message; m != "" {
			if len(m) > 200 {
				m = m[:200] + "..."
			}
			msg += ": " + m
		}
		return accountUsage{}, fmt.Errorf("%s", hide(msg))
	}
	var reply usageReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return accountUsage{}, fmt.Errorf("unreadable usage reply")
	}
	got := accountUsage{FiveHour: reply.FiveHour.window(), Weekly: reply.SevenDay.window(), Models: []usageWindow{}}
	for _, l := range reply.Limits {
		if l.Kind != "weekly_scoped" || l.Scope == nil || l.Scope.Model == nil || l.Scope.Model.DisplayName == "" {
			continue
		}
		if w := newWindow(l.Percent, l.ResetsAt); w != nil {
			w.Model = l.Scope.Model.DisplayName
			got.Models = append(got.Models, *w)
		}
	}
	return got, nil
}

// usageOf reads the listed accounts' usage side by side, each with its own token (refreshed through
// Claude Code when expired, as the proxy does). A failed account keeps its row with the error.
func usageOf(a *accounts, list []account, base, active string) []accountUsage {
	client := &http.Client{Timeout: 15 * time.Second}
	rows := make([]accountUsage, len(list))
	var wg sync.WaitGroup
	for i, acc := range list {
		wg.Add(1)
		go func(i int, acc account) {
			defer wg.Done()
			row := accountUsage{Models: []usageWindow{}}
			if token := a.token(acc); token == "" {
				row.Error = "no live login; run `claude` once under this account"
			} else if got, err := readUsage(client, base, token); err != nil {
				row.Error = err.Error()
			} else {
				row = got
			}
			row.ID, row.Email, row.Active = acc.ID, acc.Email, acc.ID == active
			rows[i] = row
		}(i, acc)
	}
	wg.Wait()
	return rows
}

// pickActive is the account the shared client's next request goes on: the proxy's choice, else the
// first account (the default login when there is one).
func pickActive(list []account, proxyChoice string) string {
	for _, acc := range list {
		if acc.ID == proxyChoice {
			return acc.ID
		}
	}
	if len(list) > 0 {
		return list[0].ID
	}
	return ""
}

// usageQuery is what `spillover usage` was asked for, and what the proxy said.
type usageQuery struct {
	all, json bool
	// proxyChoice is the proxy's active account: empty when it is stopped or has not served yet.
	proxyChoice string
	proxyUp     bool
}

func usageFlags(args []string) (q usageQuery, ok bool) {
	for _, arg := range args {
		switch arg {
		case "--all", "-all":
			q.all = true
		case "--json", "-json":
			q.json = true
		default:
			return q, false
		}
	}
	return q, true
}

const proxyDownNote = "(proxy not running: plain `claude` uses the default account)"

// showUsage prints the active account's usage, or every account's with --all; base is the API
// address. A failed account still gets its row, and the command then fails.
func showUsage(a *accounts, base string, q usageQuery, out io.Writer) error {
	list := freshAccounts(a)
	if len(list) == 0 {
		return fmt.Errorf("no accounts signed in; run `claude auth login` (default) or `spillover add`")
	}
	active := pickActive(list, q.proxyChoice)
	if !q.all {
		for _, acc := range list {
			if acc.ID == active {
				list = []account{acc}
				break
			}
		}
	}
	rows := usageOf(a, list, base, active)
	now := a.now()
	switch {
	case q.json:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		var v any = rows
		if !q.all {
			v = rows[0]
		}
		if err := enc.Encode(v); err != nil {
			return err
		}
	case q.all:
		printUsageTable(out, rows, now)
		for _, r := range rows {
			if r.Error != "" {
				fmt.Fprintf(out, "%s: %s\n", r.ID, r.Error)
			}
		}
	case rows[0].Error == "":
		printUsageOne(out, rows[0], now)
	}
	if !q.json && !q.proxyUp {
		fmt.Fprintln(out, proxyDownNote)
	}
	failed := 0
	for _, r := range rows {
		if r.Error != "" {
			failed++
		}
	}
	switch {
	case failed == 0:
		return nil
	case !q.all:
		return fmt.Errorf("%s: %s", rows[0].ID, rows[0].Error)
	default:
		return fmt.Errorf("usage not read for %d of %d accounts", failed, len(rows))
	}
}

func printUsageOne(out io.Writer, r accountUsage, now time.Time) {
	who := r.ID
	if r.Email != "" {
		who += " (" + r.Email + ")"
	}
	fmt.Fprintln(out, who)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  5-hour\t%s\t%s\n", percentText(r.FiveHour), resetText(r.FiveHour, now))
	fmt.Fprintf(tw, "  weekly\t%s\t%s\n", percentText(r.Weekly), resetText(r.Weekly, now))
	for i := range r.Models {
		m := &r.Models[i]
		fmt.Fprintf(tw, "  weekly %s\t%s\t%s\n", m.Model, percentText(m), resetText(m, now))
	}
	tw.Flush()
}

func printUsageTable(out io.Writer, rows []accountUsage, now time.Time) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  ACCOUNT\tEMAIL\t5-HOUR\tRESETS\tWEEKLY\tRESETS\tWEEKLY BY MODEL")
	for _, r := range rows {
		mark := " "
		if r.Active {
			mark = "*"
		}
		email := r.Email
		if email == "" {
			email = "(email unknown)"
		}
		if r.Error != "" {
			fmt.Fprintf(tw, "%s %s\t%s\t?\t\t?\t\t\n", mark, r.ID, email)
			continue
		}
		models := make([]string, 0, len(r.Models))
		for i := range r.Models {
			models = append(models, r.Models[i].Model+" "+percentText(&r.Models[i]))
		}
		byModel := strings.Join(models, ", ")
		if byModel == "" {
			byModel = "-"
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\t%s\t%s\t%s\n", mark, r.ID, email,
			percentText(r.FiveHour), resetText(r.FiveHour, now), percentText(r.Weekly), resetText(r.Weekly, now), byModel)
	}
	tw.Flush()
}

// statusInput is the part of Claude Code's status line input spillover reads: the windows of the
// session's last reply, from the headers of the account that answered it.
type statusInput struct {
	RateLimits struct {
		FiveHour *statusWindow `json:"five_hour"`
		SevenDay *statusWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

type statusWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
}

// percent is the window's fill as "76%"; empty when the input did not carry it.
func (w *statusWindow) percent() string {
	if w == nil || w.UsedPercentage == nil {
		return ""
	}
	return percentText(&usageWindow{Percent: *w.UsedPercentage})
}

// sessionProxy is the proxy address and client of a session's ANTHROPIC_BASE_URL; ok is false when
// the session does not send through a proxy on this machine.
func sessionProxy(baseURL string) (base, client string, ok bool) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" {
		return "", "", false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return "", "", false
	}
	client = sharedClient
	if c, _, routed := route(strings.TrimSuffix(u.Path, "/") + "/"); routed {
		client = c
	}
	return u.Scheme + "://" + u.Host, client, true
}

// statusText is the status line segment "work 5h 76% · wk 23%": the client's account and the
// windows of the session's last reply. Empty when the proxy names no account.
func statusText(in statusInput, rows []accountStatus, client string) string {
	label := activeFor(rows, client)
	if label == "" && len(rows) > 0 {
		label = rows[0].ID
	}
	if label == "" {
		return ""
	}
	var windows []string
	if p := in.RateLimits.FiveHour.percent(); p != "" {
		windows = append(windows, "5h "+p)
	}
	if p := in.RateLimits.SevenDay.percent(); p != "" {
		windows = append(windows, "wk "+p)
	}
	if len(windows) == 0 {
		return label
	}
	return label + " " + strings.Join(windows, " · ")
}

// showStatusLine prints the status line segment of the session whose status line input is in and
// whose ANTHROPIC_BASE_URL is baseURL: nothing when the session does not go through the proxy,
// "spillover: not running" when that proxy refuses the connection.
func showStatusLine(in io.Reader, baseURL string, out io.Writer) {
	var input statusInput
	if in != nil {
		_ = json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&input)
	}
	base, client, ok := sessionProxy(baseURL)
	if !ok {
		return
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(base + "/_status")
	if errors.Is(err, syscall.ECONNREFUSED) {
		fmt.Fprintln(out, "spillover: not running")
		return
	}
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var rows []accountStatus
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rows) != nil {
		return
	}
	if text := statusText(input, rows, client); text != "" {
		fmt.Fprintln(out, text)
	}
}

// percentText is a window's fill to one decimal at most ("12%", "12.5%"); "-" when the reply had no
// such window.
func percentText(w *usageWindow) string {
	if w == nil {
		return "-"
	}
	return strconv.FormatFloat(math.Round(w.Percent*10)/10, 'f', -1, 64) + "%"
}

// resetText is when a window resets, on the clock of now's zone: "Sun 03:20 (2h47m)".
func resetText(w *usageWindow, now time.Time) string {
	if w == nil || w.ResetsAt == nil {
		return "-"
	}
	return w.ResetsAt.In(now.Location()).Format("Mon 15:04") + " (" + shortDuration(w.ResetsAt.Sub(now)) + ")"
}

// shortDuration is d as "3d4h", "2h47m" or "12m"; "now" when it is not ahead.
func shortDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d <= 0 {
		return "now"
	}
	days, hours, minutes := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

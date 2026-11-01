package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const usage = `spillover [serve flags]        run the proxy (default)
spillover add [name]           sign in a new account (opens the browser)
spillover list                 show the accounts
spillover use <name>           send requests on this account from now on (until it is spent)
spillover usage [--all] [--json]
                                how full the active account (or every account) is, and when it resets
spillover remove <name> [-y]   sign an account out and delete its login

The default account is the one plain "claude auth login" signs in.`

var accountName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func runCommand(command string, args []string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	a := newAccounts(home, "auto", "")
	switch command {
	case "serve":
		serve(args)
		return 0
	case "add":
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		err = addAccount(a, name, loginAs(a), os.Stdout)
	case "list":
		listAccounts(a, os.Stdout)
	case "use":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: spillover use <name>")
			return 2
		}
		err = useAccount(proxyURL(), args[0], os.Stdout)
	case "usage":
		q, ok := usageFlags(args)
		if !ok {
			fmt.Fprintln(os.Stderr, "usage: spillover usage [--all] [--json]")
			return 2
		}
		q.proxyChoice, q.proxyUp = proxyActive()
		err = showUsage(a, envString("SPILLOVER_UPSTREAM", "https://api.anthropic.com"), q, os.Stdout)
	case "remove":
		err = removeAccount(a, args, os.Stdin, os.Stdout)
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// freshAccounts is the account list read now, not the cached one.
func freshAccounts(a *accounts) []account {
	a.mu.Lock()
	a.discovered = nil
	a.mu.Unlock()
	return a.list()
}

// nextFreeName is account-N for the first N not taken.
func nextFreeName(taken []account) string {
	used := map[string]bool{}
	for _, t := range taken {
		used[t.ID] = true
	}
	for n := 1; ; n++ {
		if name := fmt.Sprintf("account-%d", n); !used[name] {
			return name
		}
	}
}

// loginAs runs Claude Code's own browser sign-in into the config dir, with the terminal attached.
func loginAs(a *accounts) func(dir string) error {
	return func(dir string) error {
		if a.claude == "" {
			return fmt.Errorf("claude is not on PATH; install Claude Code first")
		}
		cmd := exec.Command(a.claude, "auth", "login", "--claudeai")
		cmd.Env = claudeEnv(dir)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
}

// addAccount signs a new login into ~/.claude-<name> (account-N when no name is given) and
// reports who it is. login is injected so the flow is testable without a browser.
func addAccount(a *accounts, name string, login func(dir string) error, out io.Writer) error {
	existing := freshAccounts(a)
	if name == "" {
		name = nextFreeName(existing)
	}
	if name == defaultID {
		return fmt.Errorf(`"default" is the account plain "claude auth login" signs in; pick another name`)
	}
	if !accountName.MatchString(name) {
		return fmt.Errorf("name %q: use letters, digits, - and _", name)
	}
	dir := filepath.Join(a.home, ".claude-"+name)
	_, statErr := os.Stat(dir)
	created := os.IsNotExist(statErr)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fmt.Fprintf(out, "Signing in %q. Finish the sign-in in your browser...\n", name)
	if err := login(dir); err != nil {
		if created {
			_ = os.Remove(dir)
		}
		return fmt.Errorf("sign-in failed: %w", err)
	}
	var added *account
	list := freshAccounts(a)
	for i := range list {
		if list[i].ID == name {
			added = &list[i]
		}
	}
	if added == nil {
		if created {
			_ = os.Remove(dir)
		}
		return fmt.Errorf("no login found after sign-in; nothing was added")
	}
	who := added.Email
	if who == "" {
		who = "signed in"
	}
	fmt.Fprintf(out, "Added %s (%s). %d accounts now.\n", name, who, len(list))
	for _, other := range list {
		if other.ID != name && added.Email != "" && other.Email == added.Email {
			fmt.Fprintf(out, "Warning: %s is the same login as %q; two entries for one account do not add capacity. `spillover remove %s` drops it.\n", added.Email, other.ID, name)
		}
	}
	return nil
}

func listAccounts(a *accounts, out io.Writer) {
	list := freshAccounts(a)
	if len(list) == 0 {
		fmt.Fprintln(out, "No accounts signed in. Run `claude auth login` (default) or `spillover add`.")
		return
	}
	active, up := proxyActive()
	if up {
		active = pickActive(list, active)
	}
	for _, acc := range list {
		mark := " "
		if acc.ID == active {
			mark = "*"
		}
		email := acc.Email
		if email == "" {
			email = "(email unknown)"
		}
		fmt.Fprintf(out, "%s %-16s %s\n", mark, acc.ID, email)
	}
	if !up {
		fmt.Fprintln(out, "(proxy not running: start it with the installed service, or `spillover`)")
	}
}

func proxyURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", envInt("SPILLOVER_PORT", defaultPort))
}

// useAccount tells the running proxy to send the shared client's requests on the named account.
func useAccount(base, name string, out io.Writer) error {
	body, _ := json.Marshal(map[string]string{"id": name})
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post(base+"/_use", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("proxy not running: start it with the installed service, or `spillover`")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var reply struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &reply)
		return fmt.Errorf("%s", reply.Error.Message)
	}
	fmt.Fprintf(out, "Now on %s.\n", name)
	return nil
}

// proxyActive asks the running proxy which account the shared client is on (empty before its first
// request); up is false when no proxy answers.
func proxyActive() (active string, up bool) {
	resp, err := (&http.Client{Timeout: time.Second}).Get(proxyURL() + "/_status")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return activeFromStatus(raw), true
}

func activeFromStatus(raw []byte) string {
	var rows []accountStatus
	if json.Unmarshal(raw, &rows) != nil {
		return ""
	}
	for _, r := range rows {
		for _, c := range r.ActiveFor {
			if c == sharedClient {
				return r.ID
			}
		}
	}
	return ""
}

// removeAccount deletes a non-default login: its config dir and, on macOS, its Keychain item.
func removeAccount(a *accounts, args []string, in io.Reader, out io.Writer) error {
	yes := false
	name := ""
	for _, arg := range args {
		if arg == "-y" || arg == "--yes" {
			yes = true
		} else {
			name = arg
		}
	}
	if name == "" {
		return fmt.Errorf("usage: spillover remove <name> [-y]")
	}
	if name == defaultID {
		return fmt.Errorf(`the default account is Claude Code's own login; sign out with "claude auth logout"`)
	}
	var target *account
	for _, acc := range freshAccounts(a) {
		if acc.ID == name {
			acc := acc
			target = &acc
		}
	}
	if target == nil {
		return fmt.Errorf("no account named %q", name)
	}
	if !yes {
		fmt.Fprintf(out, "Delete the login %q (%s) from this machine? [y/N] ", name, target.Email)
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
			fmt.Fprintln(out, "Left as it is.")
			return nil
		}
	}
	if runtime.GOOS == "darwin" {
		for _, query := range keychainQueries(target.Dir) {
			args := append([]string{"delete-generic-password"}, query[1:len(query)-1]...)
			_ = exec.Command("security", args...).Run()
		}
	}
	if err := os.RemoveAll(target.Dir); err != nil {
		return err
	}
	fmt.Fprintf(out, "Removed %s.\n", name)
	return nil
}

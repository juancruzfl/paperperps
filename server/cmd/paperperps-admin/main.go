// Command paperperps-admin moderates accounts through the signed admin API
// (docs/operations.md, "Admin tool").
//
//	paperperps-admin keygen                 # once: make a key pair
//	paperperps-admin find <text>            # search handles
//	paperperps-admin show <account>
//	paperperps-admin rename <account> [new-handle] --reason "…"
//	paperperps-admin hide|unhide <account> --reason "…"
//	paperperps-admin suspend|unsuspend <account> --reason "…"
//	paperperps-admin reset <account> --reason "…"
//	paperperps-admin delete <account> --reason "…" [--yes]
//	paperperps-admin log [--account <account>] [--limit 50]
//
// <account> is an account ID or an exact handle. The server comes from PAPERPERPS_URL
// (default https://paperperps.lol). The private key comes from PAPERPERPS_ADMIN_KEY, or
// the file PAPERPERPS_ADMIN_KEY_FILE (default ~/.config/paperperps/admin.key).
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/adminauth"
)

const usage = `usage: paperperps-admin <command> [arguments] [flags]

commands:
  keygen [--out FILE]                      make a key pair (prints the public key for Railway)
  pubkey                                   print the public key of your private key
  find TEXT                                search accounts by handle (or exact ID)
  show ACCOUNT                             account details, recent fills and admin actions
  rename ACCOUNT [HANDLE] --reason R       set a handle (random if HANDLE is left out)
  hide ACCOUNT --reason R                  take off the leaderboard; the user can't undo it
  unhide ACCOUNT --reason R
  suspend ACCOUNT --reason R               can close positions, can't open, reset or rename
  unsuspend ACCOUNT --reason R
  reset ACCOUNT --reason R                 fresh $1,000 run, ignoring the cooldown
  delete ACCOUNT --reason R [--yes]        delete permanently
  log [--account ACCOUNT] [--limit N]      recent admin actions

ACCOUNT is an account ID or an exact handle. Add --json to any read command for raw output.

environment:
  PAPERPERPS_URL             server (default https://paperperps.lol)
  PAPERPERPS_ADMIN_KEY       private key, e.g. from a password manager:
                               PAPERPERPS_ADMIN_KEY="$(pass show paperperps/admin)" paperperps-admin …
  PAPERPERPS_ADMIN_KEY_FILE  private key file, used when PAPERPERPS_ADMIN_KEY is unset
                             (default ~/.config/paperperps/admin.key)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type flags struct {
	reason  string
	yes     bool
	json    bool
	out     string
	account string
	limit   int
}

// parse reads flags given anywhere among the positional arguments.
func parse(args []string) (flags, []string, error) {
	var f flags
	fs := flag.NewFlagSet("paperperps-admin", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.StringVar(&f.reason, "reason", "", "why (goes in the audit log)")
	fs.BoolVar(&f.yes, "yes", false, "don't ask for confirmation")
	fs.BoolVar(&f.json, "json", false, "print raw JSON")
	fs.StringVar(&f.out, "out", "", "where keygen writes the private key")
	fs.StringVar(&f.account, "account", "", "only this account's actions")
	fs.IntVar(&f.limit, "limit", 50, "how many rows")
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return f, nil, err
		}
		if fs.NArg() == 0 {
			return f, pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		return nil
	}
	cmd := args[0]
	f, pos, err := parse(args[1:])
	if err != nil {
		return err
	}
	if cmd == "keygen" {
		return keygen(f.out)
	}

	key, err := loadKey()
	if err != nil {
		return err
	}
	if cmd == "pubkey" {
		fmt.Println(adminauth.PublicKeyOf(key))
		return nil
	}
	base, err := serverURL()
	if err != nil {
		return err
	}
	a := &admin{c: &adminauth.Client{BaseURL: base, Key: key}, f: f}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	need := func(n int, what string) error {
		if len(pos) < n {
			return fmt.Errorf("usage: paperperps-admin %s %s", cmd, what)
		}
		return nil
	}
	switch cmd {
	case "find":
		if err := need(1, "TEXT"); err != nil {
			return err
		}
		return a.find(ctx, pos[0])
	case "log":
		return a.log(ctx)
	case "show", "rename", "hide", "unhide", "suspend", "unsuspend", "reset", "delete":
		if err := need(1, "ACCOUNT"); err != nil {
			return err
		}
		id, handle, err := a.resolve(ctx, pos[0])
		if err != nil {
			return err
		}
		if cmd == "show" {
			return a.show(ctx, id)
		}
		if strings.TrimSpace(f.reason) == "" {
			return errors.New(`--reason is required: it goes in the audit log`)
		}
		return a.change(ctx, cmd, id, handle, pos[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// --- configuration ---

func defaultKeyFile() string {
	if p := os.Getenv("PAPERPERPS_ADMIN_KEY_FILE"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "admin.key"
	}
	return filepath.Join(dir, "paperperps", "admin.key")
}

func keygen(out string) error {
	if out == "" {
		out = defaultKeyFile()
	}
	priv, pub, err := adminauth.GenerateKey()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	// O_EXCL: never overwrite a key that may already be in use.
	file, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; move it away first if you really want a new key", out)
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(file, priv); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	fmt.Printf(`Private key written to %s (readable only by you). Keep it off shared machines and out of git.

Set this on the Railway service (Variables), then redeploy:

  ADMIN_PUBLIC_KEY=%s

The public key can only verify signatures, so it's safe in Railway.
`, out, pub)
	return nil
}

func loadKey() (ed25519.PrivateKey, error) {
	if k := os.Getenv("PAPERPERPS_ADMIN_KEY"); k != "" {
		return adminauth.ParsePrivateKey(k)
	}
	path := defaultKeyFile()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no private key at %s: run `paperperps-admin keygen`, or set PAPERPERPS_ADMIN_KEY", path)
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users (mode %v); run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return adminauth.ParsePrivateKey(string(data))
}

// serverURL refuses plain HTTP except to this machine: signed requests can't be forged,
// but their contents (handles, reasons) would travel in the clear.
func serverURL() (string, error) {
	raw := os.Getenv("PAPERPERPS_URL")
	if raw == "" {
		raw = "https://paperperps.lol"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("PAPERPERPS_URL %q isn't a URL", raw)
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", fmt.Errorf("PAPERPERPS_URL must be https (plain http is only allowed for localhost)")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// --- commands ---

type admin struct {
	c *adminauth.Client
	f flags
}

func (a *admin) printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func table() *tabwriter.Writer { return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0) }

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func status(hidden bool, suspendedAt *time.Time) string {
	var s []string
	if hidden {
		s = append(s, "hidden")
	}
	if suspendedAt != nil {
		s = append(s, "suspended")
	}
	if len(s) == 0 {
		return "-"
	}
	return strings.Join(s, ",")
}

func (a *admin) search(ctx context.Context, q string) ([]accounts.AccountSummary, error) {
	var rows []accounts.AccountSummary
	err := a.c.Do(ctx, "GET", "/api/admin/accounts?q="+url.QueryEscape(q)+fmt.Sprintf("&limit=%d", a.f.limit), nil, &rows)
	return rows, err
}

// resolve turns an account ID or exact handle into the account's ID and handle.
func (a *admin) resolve(ctx context.Context, ref string) (uuid.UUID, string, error) {
	rows, err := a.search(ctx, ref)
	if err != nil {
		return uuid.Nil, "", err
	}
	ref = strings.ToLower(strings.TrimSpace(ref))
	for _, r := range rows {
		if r.ID.String() == ref || r.Handle == ref {
			return r.ID, r.Handle, nil
		}
	}
	return uuid.Nil, "", fmt.Errorf("no account with ID or handle %q (try: paperperps-admin find %s)", ref, ref)
}

func (a *admin) find(ctx context.Context, q string) error {
	rows, err := a.search(ctx, q)
	if err != nil || a.f.json {
		if err == nil {
			err = a.printJSON(rows)
		}
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no matches")
		return nil
	}
	w := table()
	fmt.Fprintln(w, "ID\tHANDLE\tCREATED\tTRADES\tSTATUS")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", r.ID, r.Handle, ago(r.CreatedAt), r.Trades, status(r.LeaderboardHidden, r.SuspendedAt))
	}
	return w.Flush()
}

func (a *admin) show(ctx context.Context, id uuid.UUID) error {
	var res struct {
		Account     accounts.Account       `json:"account"`
		Summary     map[string]any         `json:"summary"`
		LifetimePnl string                 `json:"lifetimePnl"`
		RecentFills []accounts.Fill        `json:"recentFills"`
		Actions     []accounts.AdminAction `json:"actions"`
	}
	if a.f.json {
		var raw any
		if err := a.c.Do(ctx, "GET", "/api/admin/accounts/"+id.String(), nil, &raw); err != nil {
			return err
		}
		return a.printJSON(raw)
	}
	if err := a.c.Do(ctx, "GET", "/api/admin/accounts/"+id.String(), nil, &res); err != nil {
		return err
	}
	acc := res.Account
	w := table()
	fmt.Fprintf(w, "id\t%s\n", acc.ID)
	fmt.Fprintf(w, "handle\t%s\n", acc.Handle)
	fmt.Fprintf(w, "created\t%s (%s)\n", acc.CreatedAt.Format(time.RFC3339), ago(acc.CreatedAt))
	fmt.Fprintf(w, "leaderboard\topt-in %v, hidden by admin %v\n", acc.LeaderboardOptIn, acc.LeaderboardHidden)
	if acc.Suspension != nil {
		fmt.Fprintf(w, "suspended\t%s (%s): %s\n", acc.Suspension.At.Format(time.RFC3339), ago(acc.Suspension.At), acc.Suspension.Reason)
	} else {
		fmt.Fprintf(w, "suspended\tno\n")
	}
	fmt.Fprintf(w, "balance\t$%s (account value $%v)\n", acc.Balance.StringFixed(2), res.Summary["accountValue"])
	fmt.Fprintf(w, "lifetime pnl\t$%s over %d reset(s)\n", res.LifetimePnl, acc.Resets)
	fmt.Fprintf(w, "positions\t%d open, %d open orders\n", countOpen(acc), len(acc.OpenOrders))
	w.Flush()

	if len(res.RecentFills) > 0 {
		fmt.Println("\nrecent fills:")
		w = table()
		for _, f := range res.RecentFills {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s @ %s\n", ago(f.CreatedAt), f.Coin, f.Side, f.Sz, f.Px)
		}
		w.Flush()
	}
	if len(res.Actions) > 0 {
		fmt.Println("\nadmin actions:")
		printActions(res.Actions)
	}
	return nil
}

func countOpen(acc accounts.Account) int {
	n := 0
	for _, p := range acc.Positions {
		if !p.Size.IsZero() {
			n++
		}
	}
	return n
}

func printActions(rows []accounts.AdminAction) {
	w := table()
	for _, r := range rows {
		handle := ""
		if r.Handle != nil {
			handle = *r.Handle
		}
		detail := ""
		if len(r.Detail) > 0 {
			b, _ := json.Marshal(r.Detail)
			detail = " " + string(b)
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%q%s\n", r.At.Local().Format("2006-01-02 15:04"), r.Action, handle, r.Reason, detail)
	}
	w.Flush()
}

func (a *admin) log(ctx context.Context) error {
	path := fmt.Sprintf("/api/admin/actions?limit=%d", a.f.limit)
	if a.f.account != "" {
		id, _, err := a.resolve(ctx, a.f.account)
		if err != nil {
			// A deleted account can't be resolved by handle, but its ID still works.
			parsed, perr := uuid.Parse(a.f.account)
			if perr != nil {
				return err
			}
			id = parsed
		}
		path += "&account=" + id.String()
	}
	var rows []accounts.AdminAction
	if err := a.c.Do(ctx, "GET", path, nil, &rows); err != nil {
		return err
	}
	if a.f.json {
		return a.printJSON(rows)
	}
	if len(rows) == 0 {
		fmt.Println("no admin actions")
		return nil
	}
	printActions(rows)
	return nil
}

func (a *admin) change(ctx context.Context, cmd string, id uuid.UUID, handle string, rest []string) error {
	path := "/api/admin/accounts/" + id.String()
	body := map[string]any{"reason": a.f.reason}
	switch cmd {
	case "rename":
		if len(rest) > 0 {
			body["handle"] = rest[0]
		}
		var res struct{ Handle string }
		if err := a.c.Do(ctx, "POST", path+"/rename", body, &res); err != nil {
			return err
		}
		fmt.Printf("renamed %s → %s\n", handle, res.Handle)
		return nil
	case "hide", "unhide":
		body["hidden"] = cmd == "hide"
		path += "/leaderboard"
	case "suspend", "unsuspend":
		body["suspended"] = cmd == "suspend"
		path += "/suspension"
	case "reset":
		path += "/reset"
	case "delete":
		if !a.f.yes {
			fmt.Printf("Permanently delete %s (%s) and all its history? Type the handle to confirm: ", handle, id)
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.TrimSpace(line) != handle {
				return errors.New("not confirmed; nothing deleted")
			}
		}
		path += "/delete"
	}
	if err := a.c.Do(ctx, "POST", path, body, nil); err != nil {
		return err
	}
	past := map[string]string{"hide": "hidden", "unhide": "unhidden", "suspend": "suspended",
		"unsuspend": "unsuspended", "reset": "reset", "delete": "deleted"}[cmd]
	fmt.Printf("%s %s (%s)\n", past, handle, id)
	return nil
}

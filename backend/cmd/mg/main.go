// Command mg is the command-line client of micro git. It is a thin wrapper
// around the JSON API: every command is one or two HTTP requests, so it needs
// no git protocol and no libraries beyond the Go standard library.
//
//	mg login alice                      log in (password from MG_PASSWORD or a prompt)
//	mg repos                            list repositories you may see
//	mg new notes --private              create a repository
//	mg commit alice/notes -m "add readme" README.md
//	mg log alice/notes                  commit history
//	mg ls alice/notes                   list files; mg cat alice/notes README.md
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

const defaultServer = "http://localhost:8080"

// maxResponseBytes caps one API answer. A blob is at most 1 MiB of text, but
// JSON escaping can expand control characters up to 6x (\u00XX), and commit
// detail / compare answers carry diffs for many files, so 8 MiB leaves
// headroom for every legitimate answer while stopping a runaway or hostile
// server from filling memory.
const maxResponseBytes = 8 << 20

// config is what `mg login` saves, so later commands know the server and carry
// the session cookie.
type config struct {
	Server  string `json:"server"`
	Session string `json:"session"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return path.Join(dir, "mg", "config.json")
}

func loadConfig() config {
	c := config{Server: defaultServer}
	if b, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Server == "" {
		c.Server = defaultServer
	}
	return c
}

func (c config) save() error {
	if err := os.MkdirAll(path.Dir(configPath()), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(c)
	return os.WriteFile(configPath(), b, 0o600) // holds a session token: owner-only
}

// call sends one request and decodes a JSON answer into out (may be nil). A
// non-2xx answer becomes an error carrying the server's message.
func (c config) call(method, p string, body, out any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.Server, "/")+p, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Session != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: c.Session})
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s (is the server running?): %w", c.Server, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if len(raw) > maxResponseBytes {
		return resp, fmt.Errorf("response too large (over %d MiB) from %s", maxResponseBytes>>20, c.Server)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return resp, fmt.Errorf("%s (HTTP %d)", e.Error, resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp, fmt.Errorf("unexpected answer from server: %w", err)
		}
	}
	return resp, nil
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "mg: "+format+"\n", a...)
	os.Exit(1)
}

// parse is flag parsing that lets flags and positional arguments mix
// (`mg commit alice/notes -m msg file`); the standard package stops at the
// first positional argument.
func parse(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			os.Exit(2)
		}
		if fs.NArg() == 0 {
			return pos
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// splitRepo turns "alice/notes" or "alice/notes:feature" into owner, name, ref.
func splitRepo(s, defaultRef string) (owner, name, ref string) {
	ref = defaultRef
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s, ref = s[:i], s[i+1:]
	}
	o, n, ok := strings.Cut(s, "/")
	if !ok || o == "" || n == "" {
		fail("repository must look like owner/name, got %q", s)
	}
	return o, n, ref
}

func repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func prompt(label string, hide bool) string {
	fmt.Fprint(os.Stderr, label)
	if hide { // turn terminal echo off so the password is not shown
		off := exec.Command("stty", "-echo")
		off.Stdin = os.Stdin
		if off.Run() == nil {
			defer func() {
				on := exec.Command("stty", "echo")
				on.Stdin = os.Stdin
				_ = on.Run()
				fmt.Fprintln(os.Stderr)
			}()
		}
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	cfg := loadConfig()
	switch cmd {
	case "register", "login":
		cmdAuth(cfg, cmd, args)
	case "logout":
		_, _ = cfg.call("POST", "/auth/logout", nil, nil)
		cfg.Session = ""
		if err := cfg.save(); err != nil {
			fail("%v", err)
		}
		fmt.Println("logged out")
	case "whoami":
		var u struct{ Username string }
		if _, err := cfg.call("GET", "/auth/me", nil, &u); err != nil {
			fail("not logged in (%v)", err)
		}
		fmt.Println(u.Username, "@", cfg.Server)
	case "repos":
		cmdRepos(cfg, args)
	case "new":
		cmdNew(cfg, args)
	case "log":
		cmdLog(cfg, args)
	case "ls":
		cmdLs(cfg, args)
	case "cat":
		cmdCat(cfg, args)
	case "commit":
		cmdCommit(cfg, args)
	case "clone":
		cmdClone(cfg, args)
	case "pull":
		cmdPull(cfg, args)
	case "push":
		cmdPush(cfg, args)
	case "status":
		cmdStatus(args)
	case "cherry-pick":
		cmdCherryPick(cfg, args)
	case "branch":
		cmdBranch(cfg, args)
	case "merge":
		cmdMerge(cfg, args)
	case "diff":
		cmdDiff(cfg, args)
	case "show":
		cmdShow(cfg, args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "mg: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `mg - micro git command line

  mg register|login <user> [-server URL]   password: MG_PASSWORD env or prompt
  mg logout | whoami
  mg repos [-owner <user>]                 list repositories
  mg new <name> [-private] [-d <text>]     create a repository
  mg ls  <owner/repo[:ref]> [dir]          list a directory
  mg cat <owner/repo[:ref]> <file>         print a file
  mg log <owner/repo[:ref]> [-n 20]        commit history
  mg commit <owner/repo> -m <message> [-b branch] [-rm path]... <file[:path]>...
                                           commit local files (text only)

  Working copy (a directory remembers its last sync in .mg/state.json):
  mg clone <owner/repo[:branch]> [dir]     download a branch
  mg status [dir]                          local changes since the last sync
  mg pull [-f] [dir]                       fast-forward to the remote tip
  mg push -m <message> [dir]               commit all local changes
  mg cherry-pick <owner/repo> <oid> [-b branch]
                                           copy one commit onto a branch
  mg branch <owner/repo> [name [-from b] | -d name]
  mg merge <owner/repo> <branch> [-into base]   fast-forward only
  mg diff <owner/repo> <base> <head> | mg show <owner/repo> <oid>
`)
}

func cmdAuth(cfg config, cmd string, args []string) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	server := fs.String("server", cfg.Server, "server URL")
	pos := parse(fs, args)
	if len(pos) != 1 {
		fail("usage: mg %s <username> [-server URL]", cmd)
	}
	cfg.Server = *server
	pw := os.Getenv("MG_PASSWORD")
	if pw == "" {
		pw = prompt("Password: ", true)
	}
	creds := map[string]string{"username": pos[0], "password": pw}
	if cmd == "register" {
		if _, err := cfg.call("POST", "/auth/register", creds, nil); err != nil {
			fail("%v", err)
		}
		fmt.Println("registered", pos[0])
	}
	resp, err := cfg.call("POST", "/auth/login", creds, nil)
	if err != nil {
		fail("%v", err)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "session" {
			cfg.Session = ck.Value
		}
	}
	if err := cfg.save(); err != nil {
		fail("%v", err)
	}
	fmt.Println("logged in as", pos[0])
}

type repoJSON struct {
	Owner, Name, Description string
	Private, Empty           bool
}

func cmdRepos(cfg config, args []string) {
	fs := flag.NewFlagSet("repos", flag.ExitOnError)
	owner := fs.String("owner", "", "only this user's repositories")
	parse(fs, args)
	q := "/repos?per_page=100"
	if *owner != "" {
		q += "&owner=" + url.QueryEscape(*owner)
	}
	var out struct{ Repos []repoJSON }
	if _, err := cfg.call("GET", q, nil, &out); err != nil {
		fail("%v", err)
	}
	for _, r := range out.Repos {
		vis := "public"
		if r.Private {
			vis = "private"
		}
		fmt.Printf("%s/%s\t%s\t%s\n", r.Owner, r.Name, vis, r.Description)
	}
}

func cmdNew(cfg config, args []string) {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	private := fs.Bool("private", false, "only you can see it")
	desc := fs.String("d", "", "description")
	pos := parse(fs, args)
	if len(pos) != 1 {
		fail("usage: mg new <name> [-private] [-d description]")
	}
	var r repoJSON
	body := map[string]any{"name": pos[0], "description": *desc, "private": *private}
	if _, err := cfg.call("POST", "/repos", body, &r); err != nil {
		fail("%v", err)
	}
	fmt.Printf("created %s/%s\n", r.Owner, r.Name)
}

func cmdLog(cfg config, args []string) {
	fs := flag.NewFlagSet("log", flag.ExitOnError)
	n := fs.Int("n", 20, "how many commits")
	pos := parse(fs, args)
	if len(pos) != 1 {
		fail("usage: mg log <owner/repo[:branch]> [-n 20]")
	}
	owner, name, ref := splitRepo(pos[0], "")
	q := repoPath(owner, name) + fmt.Sprintf("/commits?per_page=%d", *n)
	if ref != "" {
		q += "&ref=" + url.QueryEscape(ref)
	}
	var out struct {
		Commits []struct {
			OID, Message, Author, Time string
		}
	}
	if _, err := cfg.call("GET", q, nil, &out); err != nil {
		fail("%v", err)
	}
	for _, c := range out.Commits {
		fmt.Printf("%s  %s  %s  %s\n", c.OID[:7], c.Time, c.Author, strings.SplitN(c.Message, "\n", 2)[0])
	}
}

func cmdLs(cfg config, args []string) {
	pos := parse(flag.NewFlagSet("ls", flag.ExitOnError), args)
	if len(pos) < 1 || len(pos) > 2 {
		fail("usage: mg ls <owner/repo[:ref]> [dir]")
	}
	owner, name, ref := splitRepo(pos[0], "")
	dir := ""
	if len(pos) == 2 {
		dir = strings.Trim(pos[1], "/")
	}
	ref = defaultRef(cfg, owner, name, ref)
	var out struct {
		Entries []struct{ Name, Type, OID string }
	}
	if _, err := cfg.call("GET", treePath(owner, name, "tree", ref, dir), nil, &out); err != nil {
		fail("%v", err)
	}
	for _, e := range out.Entries {
		suffix := ""
		if e.Type == "tree" {
			suffix = "/"
		}
		fmt.Printf("%s  %s%s\n", e.OID[:7], e.Name, suffix)
	}
}

func cmdCat(cfg config, args []string) {
	pos := parse(flag.NewFlagSet("cat", flag.ExitOnError), args)
	if len(pos) != 2 {
		fail("usage: mg cat <owner/repo[:ref]> <file>")
	}
	owner, name, ref := splitRepo(pos[0], "")
	ref = defaultRef(cfg, owner, name, ref)
	var out struct{ Content string }
	if _, err := cfg.call("GET", treePath(owner, name, "blob", ref, strings.Trim(pos[1], "/")), nil, &out); err != nil {
		fail("%v", err)
	}
	fmt.Print(out.Content)
}

func treePath(owner, name, kind, ref, p string) string {
	s := repoPath(owner, name) + "/" + kind + "/" + url.PathEscape(ref)
	if p != "" {
		s += "/" + p
	}
	return s
}

// defaultRef fills in the repository's default branch when no ref was given.
func defaultRef(cfg config, owner, name, ref string) string {
	if ref != "" {
		return ref
	}
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := cfg.call("GET", repoPath(owner, name), nil, &r); err != nil {
		fail("%v", err)
	}
	return r.DefaultBranch
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func cmdCommit(cfg config, args []string) {
	fs := flag.NewFlagSet("commit", flag.ExitOnError)
	msg := fs.String("m", "", "commit message (required)")
	branch := fs.String("b", "", "branch (default: the repository's default branch)")
	var rm listFlag
	fs.Var(&rm, "rm", "delete this path in the repository (repeatable)")
	pos := parse(fs, args)
	if len(pos) < 1 || *msg == "" || (len(pos) == 1 && len(rm) == 0) {
		fail("usage: mg commit <owner/repo> -m <message> [-b branch] [-rm path]... <file[:path]>...")
	}
	owner, name, _ := splitRepo(pos[0], "")

	// 1. Where does the branch point right now? The server only accepts our
	//    commit if that is still true when it arrives (optimistic concurrency).
	var repo struct {
		DefaultBranch string `json:"default_branch"`
		Empty         bool
	}
	if _, err := cfg.call("GET", repoPath(owner, name), nil, &repo); err != nil {
		fail("%v", err)
	}
	if *branch == "" {
		*branch = repo.DefaultBranch
	}
	tip := ""
	if !repo.Empty {
		var bl struct{ Branches []struct{ Name, OID string } }
		if _, err := cfg.call("GET", repoPath(owner, name)+"/branches", nil, &bl); err != nil {
			fail("%v", err)
		}
		for _, b := range bl.Branches {
			if b.Name == *branch {
				tip = b.OID
			}
		}
		if tip == "" {
			fail("branch %q does not exist", *branch)
		}
	}

	// 2. Turn local files into "changes": full new content per path.
	var changes []map[string]any
	for _, spec := range pos[1:] {
		local, remote := spec, spec
		if i := strings.Index(spec, ":"); i > 0 {
			local, remote = spec[:i], spec[i+1:]
		}
		remote = path.Clean(strings.TrimPrefix(remote, "./"))
		if path.IsAbs(remote) || remote == ".." || strings.HasPrefix(remote, "../") {
			fail("%q: repository paths must be relative and stay inside the repository", remote)
		}
		data, err := os.ReadFile(local)
		if err != nil {
			fail("%v", err)
		}
		changes = append(changes, map[string]any{"path": remote, "content": string(data)})
	}
	for _, p := range rm {
		changes = append(changes, map[string]any{"path": p, "delete": true})
	}

	// 3. Send it. The server builds blob + tree + commit objects and moves the branch.
	var out struct{ OID, Branch string }
	body := map[string]any{"branch": *branch, "expected_tip": tip, "message": *msg, "changes": changes}
	if _, err := cfg.call("POST", repoPath(owner, name)+"/commits", body, &out); err != nil {
		if errors.Is(err, io.EOF) {
			fail("empty answer")
		}
		fail("%v", err)
	}
	fmt.Printf("[%s %s] %s\n", out.Branch, out.OID[:7], *msg)
}

package main

// Working-copy commands: clone, pull, push, status, cherry-pick, branch, merge,
// diff and show. They are built on the same JSON API as everything else in mg.
// The server has no git protocol, so a "working copy" is just a directory with
// a `.mg/state.json` that remembers which commit it was last in sync with and
// the hash of every file at that moment. That is enough to tell what changed
// locally (status/push) and what would be overwritten (pull).

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	stateDir       = ".mg"
	maxChanges     = 50      // the server accepts at most 50 changes per commit
	maxFileBytes   = 1 << 20 // and files of at most 1 MiB
	maxMessageRune = 500
	blobWorkers    = 6 // concurrent blob requests in clone and pull
)

// state is saved in <dir>/.mg/state.json after every clone, pull and push.
type state struct {
	Repo   string            `json:"repo"` // owner/name
	Branch string            `json:"branch"`
	Tip    string            `json:"tip"` // commit the files below belong to; "" for an empty repository
	Files  map[string]string `json:"files"`
}

func (s state) owner() (string, string) {
	o, n, _ := strings.Cut(s.Repo, "/")
	return o, n
}

func loadState(dir string) (state, error) {
	b, err := os.ReadFile(filepath.Join(dir, stateDir, "state.json"))
	if err != nil {
		return state{}, fmt.Errorf("%s is not an mg working copy (no %s/state.json); use mg clone", dir, stateDir)
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		return state{}, fmt.Errorf("broken %s/state.json: %w", stateDir, err)
	}
	if s.Files == nil {
		s.Files = map[string]string{}
	}
	return s, nil
}

func (s state) save(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, stateDir), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, stateDir, "state.json"), b, 0o644)
}

func hashOf(content []byte) string {
	h := sha1.Sum(content)
	return hex.EncodeToString(h[:])
}

// scanLocal returns the hash of every text file under dir. Files the server
// would refuse (binary, over 1 MiB) are skipped, and so are .git, .mg and
// node_modules.
func scanLocal(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", stateDir, "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if len(b) > maxFileBytes || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
			fmt.Fprintf(os.Stderr, "mg: skipping %s (binary or over 1 MiB)\n", rel)
			return nil
		}
		out[filepath.ToSlash(rel)] = hashOf(b)
		return nil
	})
	return out, err
}

// localChanges compares what is on disk with what the last sync recorded.
func localChanges(base, now map[string]string) (added, modified, deleted []string) {
	for p, h := range now {
		old, ok := base[p]
		switch {
		case !ok:
			added = append(added, p)
		case old != h:
			modified = append(modified, p)
		}
	}
	for p := range base {
		if _, ok := now[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)
	return
}

// escPath escapes each segment of a repository path for use in a URL.
func escPath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func isStatus(resp *http.Response, code int) bool { return resp != nil && resp.StatusCode == code }

// branchTip returns the tip of a branch, or "" if the repository is empty or
// the branch does not exist.
func branchTip(cfg config, owner, name, branch string) (string, error) {
	var bl struct{ Branches []struct{ Name, OID string } }
	if _, err := cfg.call("GET", repoPath(owner, name)+"/branches", nil, &bl); err != nil {
		return "", err
	}
	for _, b := range bl.Branches {
		if b.Name == branch {
			return b.OID, nil
		}
	}
	return "", nil
}

// readBlob returns a file's content at ref; found is false if the path does not exist there.
func readBlob(cfg config, owner, name, ref, p string) (content string, found bool, err error) {
	var out struct{ Content string }
	resp, err := cfg.call("GET", treePath(owner, name, "blob", ref, escPath(p)), nil, &out)
	if err != nil {
		if isStatus(resp, http.StatusNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return out.Content, true, nil
}

// listFiles walks the tree at ref and returns every file path.
func listFiles(cfg config, owner, name, ref, dir string) ([]string, error) {
	var out struct {
		Entries []struct{ Name, Type string }
	}
	if _, err := cfg.call("GET", treePath(owner, name, "tree", ref, escPath(dir)), nil, &out); err != nil {
		return nil, err
	}
	var files []string
	for _, e := range out.Entries {
		if _, err := safeRel(e.Name); err != nil || strings.Contains(e.Name, "/") {
			return nil, fmt.Errorf("refusing unsafe tree entry name %q from the server", e.Name)
		}
		p := path.Join(dir, e.Name)
		if e.Type == "tree" {
			sub, err := listFiles(cfg, owner, name, ref, p)
			if err != nil {
				return nil, err
			}
			files = append(files, sub...)
		} else {
			files = append(files, p)
		}
	}
	return files, nil
}

// safeRel checks a repository path that came from the server and returns it
// unchanged if it is safe to join onto the working directory: relative, no
// empty, "." or ".." segment, no .git or .mg segment, no backslash, no NUL.
func safeRel(p string) (string, error) {
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("refusing unsafe path %q from the server: %s", p, why)
	}
	switch {
	case p == "":
		return bad("empty")
	case strings.ContainsRune(p, 0):
		return bad("contains a NUL byte")
	case strings.Contains(p, `\`):
		return bad("contains a backslash")
	case strings.HasPrefix(p, "/"):
		return bad("absolute")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return bad("empty, '.' or '..' segment")
		}
		// Case-insensitive file systems (macOS, Windows) and NTFS ignore
		// trailing dots and spaces, so ".MG" and ".git " name the same dir.
		name := strings.TrimRight(seg, ". ")
		if name == "" || name != seg {
			return bad("segment ends in a dot or space")
		}
		if strings.EqualFold(seg, ".git") || strings.EqualFold(seg, stateDir) {
			return bad("reserved " + seg + " segment")
		}
	}
	return p, nil
}

// checkWritable fails if rel, or any directory above it, is a symlink in the
// working copy. os.Root already blocks links that leave the directory; this
// also refuses links that stay inside it, so we never write through a link.
func checkWritable(root *os.Root, rel string) error {
	parts := strings.Split(rel, "/")
	for i := range parts {
		sub := strings.Join(parts[:i+1], "/")
		fi, err := root.Lstat(filepath.FromSlash(sub))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // nothing below a missing component can be a link
			}
			return fmt.Errorf("cannot write %s: %w", rel, err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("cannot write %s: %s is a symbolic link", rel, sub)
		}
	}
	return nil
}

// writeLocal writes rel under root; root refuses any path that escapes it.
func writeLocal(root *os.Root, rel, content string) error {
	if err := checkWritable(root, rel); err != nil {
		return err
	}
	local := filepath.FromSlash(rel)
	if d := filepath.Dir(local); d != "." {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return root.WriteFile(local, []byte(content), 0o644)
}

// removeLocal deletes rel under root; a missing file is not an error.
func removeLocal(root *os.Root, rel string) error {
	if err := checkWritable(root, rel); err != nil {
		return err
	}
	if err := root.Remove(filepath.FromSlash(rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// checkPaths validates every server path before anything is written.
func checkPaths(dir string, files []string) error {
	for _, f := range files {
		if _, err := safeRel(f); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer root.Close()
	for _, f := range files {
		if err := checkWritable(root, f); err != nil {
			return err
		}
	}
	return nil
}

// fetchBlobs reads every file at ref with a small pool of workers and returns
// path -> content. Errors are deterministic: the failure of the earliest file
// in the list wins, whatever order the answers arrive in. Jobs are handed out
// in list order and a job is skipped only when an earlier one already failed,
// so no earlier job can be missed. Nothing touches the disk here, which lets
// callers write only after every blob has arrived.
func fetchBlobs(cfg config, owner, name, ref string, files []string) (map[string]string, error) {
	contents := make([]string, len(files))
	errs := make([]error, len(files))
	var (
		mu       sync.Mutex
		next     int
		firstBad = len(files) // lowest index that failed so far
		wg       sync.WaitGroup
	)
	for w := 0; w < min(blobWorkers, len(files)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i := next
				next++
				skip := i >= len(files) || i > firstBad
				mu.Unlock()
				if i >= len(files) {
					return
				}
				if skip {
					continue
				}
				c, found, err := readBlob(cfg, owner, name, ref, files[i])
				if err == nil && !found {
					err = errors.New("not found")
				}
				if err != nil {
					errs[i] = fmt.Errorf("cannot read %s: %v", files[i], err)
					mu.Lock()
					firstBad = min(firstBad, i)
					mu.Unlock()
					continue
				}
				contents[i] = c
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	out := make(map[string]string, len(files))
	for i, f := range files {
		out[f] = contents[i]
	}
	return out, nil
}

// doClone downloads a branch into dir and records the sync state.
func doClone(cfg config, spec, dir string) (string, error) {
	owner, name, branch := splitRepo(spec, "")
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := cfg.call("GET", repoPath(owner, name), nil, &repo); err != nil {
		return "", err
	}
	if branch == "" {
		branch = repo.DefaultBranch
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("%s already exists and is not empty", dir)
	}
	st := state{Repo: owner + "/" + name, Branch: branch, Files: map[string]string{}}
	tip, err := branchTip(cfg, owner, name, branch)
	if err != nil {
		return "", err
	}
	if tip == "" && branch != repo.DefaultBranch {
		return "", fmt.Errorf("branch %q does not exist", branch)
	}
	st.Tip = tip
	if tip != "" {
		files, err := listFiles(cfg, owner, name, tip, "")
		if err != nil {
			return "", err
		}
		if err := checkPaths(dir, files); err != nil {
			return "", err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return "", err
		}
		defer root.Close()
		blobs, err := fetchBlobs(cfg, owner, name, tip, files)
		if err != nil {
			return "", err
		}
		for _, f := range files {
			if err := writeLocal(root, f, blobs[f]); err != nil {
				return "", err
			}
			st.Files[f] = hashOf([]byte(blobs[f]))
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := st.save(dir); err != nil {
		return "", err
	}
	return fmt.Sprintf("cloned %s/%s:%s into %s (%d files)", owner, name, branch, dir, len(st.Files)), nil
}

// doPull fast-forwards the working copy to the remote tip. It refuses to
// overwrite a file you changed locally unless force is set.
func doPull(cfg config, dir string, force bool) (string, error) {
	st, err := loadState(dir)
	if err != nil {
		return "", err
	}
	owner, name := st.owner()
	tip, err := branchTip(cfg, owner, name, st.Branch)
	if err != nil {
		return "", err
	}
	if tip == "" || tip == st.Tip {
		return "Already up to date.", nil
	}
	var files []string
	status := map[string]string{}
	if st.Tip == "" { // we cloned an empty repository: everything is new
		all, err := listFiles(cfg, owner, name, tip, "")
		if err != nil {
			return "", err
		}
		for _, f := range all {
			files = append(files, f)
			status[f] = "added"
		}
	} else {
		var cmp struct {
			Files []struct{ Path, Status string }
		}
		q := repoPath(owner, name) + "/compare?base=" + url.QueryEscape(st.Tip) + "&head=" + url.QueryEscape(tip)
		if _, err := cfg.call("GET", q, nil, &cmp); err != nil {
			return "", err
		}
		for _, f := range cmp.Files {
			files = append(files, f.Path)
			status[f.Path] = f.Status
		}
	}
	if err := checkPaths(dir, files); err != nil {
		return "", err
	}
	now, err := scanLocal(dir)
	if err != nil {
		return "", err
	}
	if !force {
		var clash []string
		for _, f := range files {
			if now[f] != st.Files[f] { // edited, created or removed locally since the last sync
				clash = append(clash, f)
			}
		}
		if len(clash) > 0 {
			return "", fmt.Errorf("local changes would be overwritten: %s\ncommit them with mg push, undo them, or use mg pull -f", strings.Join(clash, ", "))
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	var toFetch []string
	for _, f := range files {
		if status[f] != "deleted" {
			toFetch = append(toFetch, f)
		}
	}
	blobs, err := fetchBlobs(cfg, owner, name, tip, toFetch)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if status[f] == "deleted" {
			if err := removeLocal(root, f); err != nil {
				return "", err
			}
			delete(st.Files, f)
			continue
		}
		if err := writeLocal(root, f, blobs[f]); err != nil {
			return "", err
		}
		st.Files[f] = hashOf([]byte(blobs[f]))
	}
	st.Tip = tip
	if err := st.save(dir); err != nil {
		return "", err
	}
	return fmt.Sprintf("Updated to %s (%d files changed)", tip[:7], len(files)), nil
}

// doPush sends every local change since the last sync as one commit (or
// several, when there are more than 50 changed files).
func doPush(cfg config, dir, message string) (string, error) {
	st, err := loadState(dir)
	if err != nil {
		return "", err
	}
	owner, name := st.owner()
	now, err := scanLocal(dir)
	if err != nil {
		return "", err
	}
	added, modified, deleted := localChanges(st.Files, now)
	if len(added)+len(modified)+len(deleted) == 0 {
		return "Nothing to push.", nil
	}
	tip, err := branchTip(cfg, owner, name, st.Branch)
	if err != nil {
		return "", err
	}
	if tip != st.Tip {
		return "", errors.New("the remote branch has new commits; run mg pull first")
	}
	var changes []map[string]any
	for _, p := range append(added, modified...) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return "", err
		}
		changes = append(changes, map[string]any{"path": p, "content": string(b)})
	}
	for _, p := range deleted {
		changes = append(changes, map[string]any{"path": p, "delete": true})
	}
	parts := (len(changes) + maxChanges - 1) / maxChanges
	var last string
	for i := 0; i < parts; i++ {
		chunk := changes[i*maxChanges : min((i+1)*maxChanges, len(changes))]
		msg := message
		if parts > 1 {
			msg = fmt.Sprintf("%s (part %d/%d)", message, i+1, parts)
		}
		var out struct{ OID string }
		body := map[string]any{"branch": st.Branch, "expected_tip": tip, "message": msg, "changes": chunk}
		if _, err := cfg.call("POST", repoPath(owner, name)+"/commits", body, &out); err != nil {
			return "", err
		}
		tip, last = out.OID, out.OID
		for _, c := range chunk { // remember what was sent so a failed later part can be retried
			p := c["path"].(string)
			if c["delete"] == true {
				delete(st.Files, p)
			} else {
				st.Files[p] = now[p]
			}
		}
		st.Tip = tip
		if err := st.save(dir); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("[%s %s] %s (%d files)", st.Branch, last[:7], message, len(changes)), nil
}

func doStatus(dir string) (string, error) {
	st, err := loadState(dir)
	if err != nil {
		return "", err
	}
	now, err := scanLocal(dir)
	if err != nil {
		return "", err
	}
	added, modified, deleted := localChanges(st.Files, now)
	var b strings.Builder
	tip := st.Tip
	if tip == "" {
		tip = "(empty)"
	} else {
		tip = tip[:7]
	}
	fmt.Fprintf(&b, "%s on branch %s at %s\n", st.Repo, st.Branch, tip)
	for _, p := range added {
		fmt.Fprintf(&b, "  A  %s\n", p)
	}
	for _, p := range modified {
		fmt.Fprintf(&b, "  M  %s\n", p)
	}
	for _, p := range deleted {
		fmt.Fprintf(&b, "  D  %s\n", p)
	}
	if len(added)+len(modified)+len(deleted) == 0 {
		b.WriteString("clean: nothing to push\n")
	}
	return b.String(), nil
}

type fileDiff struct {
	Path, Status         string
	Additions, Deletions int
	Truncated            bool
	Hunks                []struct {
		OldStart int `json:"old_start"`
		OldLines int `json:"old_lines"`
		NewStart int `json:"new_start"`
		NewLines int `json:"new_lines"`
		Lines    []struct{ Type, Text string }
	}
}

func formatDiff(files []fileDiff) string {
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "%s %s (+%d -%d)\n", f.Status, f.Path, f.Additions, f.Deletions)
		if f.Truncated {
			b.WriteString("  (too large to diff)\n")
		}
		for _, h := range f.Hunks {
			fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
			for _, l := range h.Lines {
				prefix := " "
				switch l.Type {
				case "add":
					prefix = "+"
				case "del":
					prefix = "-"
				}
				b.WriteString(prefix + l.Text + "\n")
			}
		}
	}
	return b.String()
}

// doCherryPick copies one commit's changes onto the tip of another branch as a
// new commit. A file is a conflict when the target branch has changed it since
// the picked commit's parent; nothing is committed in that case.
func doCherryPick(cfg config, spec, oid, branch string) (string, error) {
	owner, name, _ := splitRepo(spec, "")
	if branch == "" {
		branch = defaultRef(cfg, owner, name, "")
	}
	var detail struct {
		OID, Message string
		Parents      []string
		Files        []struct{ Path, Status string }
	}
	if _, err := cfg.call("GET", repoPath(owner, name)+"/commits/"+url.PathEscape(oid), nil, &detail); err != nil {
		return "", err
	}
	parent := ""
	if len(detail.Parents) > 0 {
		parent = detail.Parents[0]
	}
	tip, err := branchTip(cfg, owner, name, branch)
	if err != nil {
		return "", err
	}
	if tip == "" {
		return "", fmt.Errorf("branch %q does not exist", branch)
	}
	var changes []map[string]any
	var conflicts []string
	for _, f := range detail.Files {
		cur, curOK, err := readBlob(cfg, owner, name, tip, f.Path)
		if err != nil {
			return "", err
		}
		old, oldOK := "", false
		if parent != "" && f.Status != "added" {
			if old, oldOK, err = readBlob(cfg, owner, name, parent, f.Path); err != nil {
				return "", err
			}
		}
		if f.Status == "deleted" {
			switch {
			case !curOK: // already gone
			case oldOK && cur == old:
				changes = append(changes, map[string]any{"path": f.Path, "delete": true})
			default:
				conflicts = append(conflicts, f.Path)
			}
			continue
		}
		next, found, err := readBlob(cfg, owner, name, detail.OID, f.Path)
		if err != nil || !found {
			return "", fmt.Errorf("cannot read %s at %s: %v", f.Path, detail.OID[:7], err)
		}
		switch {
		case curOK && cur == next: // already applied
		case f.Status == "added" && curOK, f.Status == "modified" && (!curOK || cur != old):
			conflicts = append(conflicts, f.Path)
		default:
			changes = append(changes, map[string]any{"path": f.Path, "content": next})
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return "", fmt.Errorf("cherry-pick conflicts in: %s (the %s branch changed these files)", strings.Join(conflicts, ", "), branch)
	}
	if len(changes) == 0 {
		return "Nothing to cherry-pick (already applied).", nil
	}
	if len(changes) > maxChanges {
		return "", fmt.Errorf("commit touches %d files; the server accepts at most %d per commit", len(changes), maxChanges)
	}
	suffix := "\n\n(cherry picked from commit " + detail.OID + ")"
	msg := strings.TrimSpace(detail.Message)
	if r := []rune(msg); len(r) > maxMessageRune-len([]rune(suffix)) {
		msg = string(r[:maxMessageRune-len([]rune(suffix))])
	}
	var out struct{ OID, Branch string }
	body := map[string]any{"branch": branch, "expected_tip": tip, "message": msg + suffix, "changes": changes}
	if _, err := cfg.call("POST", repoPath(owner, name)+"/commits", body, &out); err != nil {
		return "", err
	}
	return fmt.Sprintf("[%s %s] %s", out.Branch, out.OID[:7], strings.SplitN(msg, "\n", 2)[0]), nil
}

// ---- command wrappers ----

func cmdClone(cfg config, args []string) {
	pos := parse(flag.NewFlagSet("clone", flag.ExitOnError), args)
	if len(pos) < 1 || len(pos) > 2 {
		fail("usage: mg clone <owner/repo[:branch]> [dir]")
	}
	dir := ""
	if len(pos) == 2 {
		dir = pos[1]
	} else {
		_, n, _ := splitRepo(pos[0], "")
		dir = n
	}
	report(doClone(cfg, pos[0], dir))
}

func cmdPull(cfg config, args []string) {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	force := fs.Bool("f", false, "overwrite local changes")
	pos := parse(fs, args)
	report(doPull(cfg, dirArg(pos, "pull"), *force))
}

func cmdPush(cfg config, args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	msg := fs.String("m", "", "commit message (required)")
	pos := parse(fs, args)
	if *msg == "" {
		fail("usage: mg push -m <message> [dir]")
	}
	report(doPush(cfg, dirArg(pos, "push -m <message>"), *msg))
}

func cmdStatus(args []string) {
	pos := parse(flag.NewFlagSet("status", flag.ExitOnError), args)
	report(doStatus(dirArg(pos, "status")))
}

func cmdCherryPick(cfg config, args []string) {
	fs := flag.NewFlagSet("cherry-pick", flag.ExitOnError)
	branch := fs.String("b", "", "branch to apply the commit to (default: the default branch)")
	pos := parse(fs, args)
	if len(pos) != 2 {
		fail("usage: mg cherry-pick <owner/repo> <commit-oid> [-b branch]  (full 40-character oid)")
	}
	report(doCherryPick(cfg, pos[0], pos[1], *branch))
}

func dirArg(pos []string, usage string) string {
	switch len(pos) {
	case 0:
		return "."
	case 1:
		return pos[0]
	}
	fail("usage: mg %s [dir]", usage)
	return ""
}

func report(msg string, err error) {
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(strings.TrimRight(msg, "\n"))
}

func cmdBranch(cfg config, args []string) {
	fs := flag.NewFlagSet("branch", flag.ExitOnError)
	from := fs.String("from", "", "create the branch from this branch (default: the default branch)")
	del := fs.Bool("d", false, "delete the branch")
	pos := parse(fs, args)
	if len(pos) < 1 || len(pos) > 2 || (*del && len(pos) != 2) {
		fail("usage: mg branch <owner/repo> [name [-from b] | -d name]")
	}
	owner, name, _ := splitRepo(pos[0], "")
	switch {
	case len(pos) == 1:
		var repo struct {
			DefaultBranch string `json:"default_branch"`
		}
		var bl struct{ Branches []struct{ Name, OID string } }
		if _, err := cfg.call("GET", repoPath(owner, name), nil, &repo); err != nil {
			fail("%v", err)
		}
		if _, err := cfg.call("GET", repoPath(owner, name)+"/branches", nil, &bl); err != nil {
			fail("%v", err)
		}
		for _, b := range bl.Branches {
			mark := " "
			if b.Name == repo.DefaultBranch {
				mark = "*"
			}
			fmt.Printf("%s %s  %s\n", mark, b.OID[:7], b.Name)
		}
	case *del:
		if _, err := cfg.call("DELETE", repoPath(owner, name)+"/branches/"+escPath(pos[1]), nil, nil); err != nil {
			fail("%v", err)
		}
		fmt.Println("deleted branch", pos[1])
	default:
		src := defaultRef(cfg, owner, name, *from)
		if _, err := cfg.call("POST", repoPath(owner, name)+"/branches", map[string]string{"name": pos[1], "from": src}, nil); err != nil {
			fail("%v", err)
		}
		fmt.Printf("created branch %s from %s\n", pos[1], src)
	}
}

func cmdMerge(cfg config, args []string) {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	into := fs.String("into", "", "branch that moves (default: the default branch)")
	pos := parse(fs, args)
	if len(pos) != 2 {
		fail("usage: mg merge <owner/repo> <branch> [-into base]  (fast-forward only)")
	}
	owner, name, _ := splitRepo(pos[0], "")
	base := defaultRef(cfg, owner, name, *into)
	var out struct {
		Branch, OID string
		PreviousOID string `json:"previous_oid"`
	}
	if _, err := cfg.call("POST", repoPath(owner, name)+"/merge", map[string]string{"base": base, "head": pos[1]}, &out); err != nil {
		fail("%v", err)
	}
	fmt.Printf("fast-forwarded %s %s..%s\n", out.Branch, out.PreviousOID[:7], out.OID[:7])
}

func cmdDiff(cfg config, args []string) {
	pos := parse(flag.NewFlagSet("diff", flag.ExitOnError), args)
	if len(pos) != 3 {
		fail("usage: mg diff <owner/repo> <base> <head>   (branch names or full oids)")
	}
	owner, name, _ := splitRepo(pos[0], "")
	var out struct{ Files []fileDiff }
	q := repoPath(owner, name) + "/compare?base=" + url.QueryEscape(pos[1]) + "&head=" + url.QueryEscape(pos[2])
	if _, err := cfg.call("GET", q, nil, &out); err != nil {
		fail("%v", err)
	}
	fmt.Print(formatDiff(out.Files))
}

func cmdShow(cfg config, args []string) {
	pos := parse(flag.NewFlagSet("show", flag.ExitOnError), args)
	if len(pos) != 2 {
		fail("usage: mg show <owner/repo> <commit-oid>  (full 40-character oid)")
	}
	owner, name, _ := splitRepo(pos[0], "")
	var out struct {
		OID, Message, Author, Time string
		Files                      []fileDiff
	}
	if _, err := cfg.call("GET", repoPath(owner, name)+"/commits/"+url.PathEscape(pos[1]), nil, &out); err != nil {
		fail("%v", err)
	}
	fmt.Printf("commit %s\nAuthor: %s\nDate:   %s\n\n    %s\n\n%s", out.OID, out.Author, out.Time, strings.ReplaceAll(out.Message, "\n", "\n    "), formatDiff(out.Files))
}

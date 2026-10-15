// Package gitobj serializes, hashes and parses git blob, tree and commit
// objects in git's exact format, so oids match real git. It has no database
// or HTTP dependencies; timestamps are supplied by the caller.
package gitobj

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Type is a git object type.
type Type string

// Object types.
const (
	TypeBlob   Type = "blob"
	TypeTree   Type = "tree"
	TypeCommit Type = "commit"
)

// Tree entry modes. The directory mode has no leading zero, as in git.
const (
	ModeFile = "100644"
	ModeDir  = "40000"
)

// OIDLen is the length of an oid in hex characters.
const OIDLen = 40

// Hash returns sha1("<type> <len>\0" + body) as 40 lowercase hex characters.
func Hash(t Type, body []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", t, len(body))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// HashBlob returns the oid of a blob with the given content.
func HashBlob(content []byte) string { return Hash(TypeBlob, content) }

// ValidOID reports whether s is 40 lowercase hex characters.
func ValidOID(s string) bool {
	if len(s) != OIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// TreeEntry is one entry of a tree object.
type TreeEntry struct {
	Mode string // ModeFile or ModeDir
	Name string
	OID  string // 40 lowercase hex characters
}

// IsDir reports whether the entry is a directory.
func (e TreeEntry) IsDir() bool { return e.Mode == ModeDir }

// SortTreeEntries sorts entries in place in git's tree order: byte-wise by
// name, where a directory compares as name + "/". So file "foo.txt" sorts
// before directory "foo" ('.' < '/'), unlike plain name order.
//
// This is NOT the API listing order (directories first, then files by name);
// that order belongs to the tree endpoint and must not be used for hashing.
func SortTreeEntries(entries []TreeEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return sortKey(entries[i]) < sortKey(entries[j])
	})
}

func sortKey(e TreeEntry) string {
	if e.IsDir() {
		return e.Name + "/"
	}
	return e.Name
}

// SerializeTree returns the tree body: entries sorted git-style, each as
// "<mode> <name>\0<20-byte oid>". The input slice is not modified.
func SerializeTree(entries []TreeEntry) ([]byte, error) {
	sorted := append([]TreeEntry(nil), entries...)
	SortTreeEntries(sorted)
	var buf bytes.Buffer
	for i, e := range sorted {
		if e.Mode != ModeFile && e.Mode != ModeDir {
			return nil, fmt.Errorf("gitobj: invalid mode %q", e.Mode)
		}
		if e.Name == "" || strings.ContainsAny(e.Name, "/\x00") {
			return nil, fmt.Errorf("gitobj: invalid entry name %q", e.Name)
		}
		if i > 0 && sorted[i-1].Name == e.Name {
			return nil, fmt.Errorf("gitobj: duplicate entry name %q", e.Name)
		}
		raw, err := decodeOID(e.OID)
		if err != nil {
			return nil, err
		}
		buf.WriteString(e.Mode)
		buf.WriteByte(' ')
		buf.WriteString(e.Name)
		buf.WriteByte(0)
		buf.Write(raw)
	}
	return buf.Bytes(), nil
}

// ParseTree parses a tree body. It returns an error on malformed input.
func ParseTree(body []byte) ([]TreeEntry, error) {
	var entries []TreeEntry
	for len(body) > 0 {
		sp := bytes.IndexByte(body, ' ')
		if sp < 0 {
			return nil, errors.New("gitobj: tree entry has no mode terminator")
		}
		mode := string(body[:sp])
		if mode != ModeFile && mode != ModeDir {
			return nil, fmt.Errorf("gitobj: invalid tree entry mode %q", mode)
		}
		rest := body[sp+1:]
		nul := bytes.IndexByte(rest, 0)
		if nul < 0 {
			return nil, errors.New("gitobj: tree entry has no NUL after name")
		}
		name := string(rest[:nul])
		if name == "" || strings.Contains(name, "/") {
			return nil, fmt.Errorf("gitobj: invalid tree entry name %q", name)
		}
		rest = rest[nul+1:]
		if len(rest) < sha1.Size {
			return nil, errors.New("gitobj: truncated tree entry oid")
		}
		entries = append(entries, TreeEntry{Mode: mode, Name: name, OID: hex.EncodeToString(rest[:sha1.Size])})
		body = rest[sha1.Size:]
	}
	return entries, nil
}

// Signature is the author or committer of a commit.
type Signature struct {
	Name  string
	Email string
	Unix  int64  // seconds since the epoch, supplied by the caller
	TZ    string // e.g. "+0000"; empty means "+0000"
}

func (s Signature) String() string {
	tz := s.TZ
	if tz == "" {
		tz = "+0000"
	}
	return fmt.Sprintf("%s <%s> %d %s", s.Name, s.Email, s.Unix, tz)
}

func parseSignature(s string) (Signature, error) {
	lt := strings.LastIndex(s, " <")
	gt := strings.LastIndex(s, "> ")
	if lt < 0 || gt < lt {
		return Signature{}, fmt.Errorf("gitobj: malformed signature %q", s)
	}
	fields := strings.Fields(s[gt+2:])
	if len(fields) != 2 {
		return Signature{}, fmt.Errorf("gitobj: malformed signature time %q", s)
	}
	unix, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return Signature{}, fmt.Errorf("gitobj: malformed signature time %q", s)
	}
	return Signature{Name: s[:lt], Email: s[lt+2 : gt], Unix: unix, TZ: fields[1]}, nil
}

// Commit is a commit object.
type Commit struct {
	Tree      string
	Parents   []string // in order
	Author    Signature
	Committer Signature
	Message   string // serialized trimmed with exactly one trailing "\n"
}

// SerializeCommit returns the commit body. The message is trimmed and stored
// with exactly one trailing newline.
func SerializeCommit(c Commit) ([]byte, error) {
	if !ValidOID(c.Tree) {
		return nil, fmt.Errorf("gitobj: invalid tree oid %q", c.Tree)
	}
	var b strings.Builder
	b.WriteString("tree " + c.Tree + "\n")
	for _, p := range c.Parents {
		if !ValidOID(p) {
			return nil, fmt.Errorf("gitobj: invalid parent oid %q", p)
		}
		b.WriteString("parent " + p + "\n")
	}
	b.WriteString("author " + c.Author.String() + "\n")
	b.WriteString("committer " + c.Committer.String() + "\n")
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(c.Message) + "\n")
	return []byte(b.String()), nil
}

// ParseCommit parses a commit body. It returns an error on malformed input
// (for example a commit without a tree line).
func ParseCommit(body []byte) (Commit, error) {
	var c Commit
	head, msg, found := strings.Cut(string(body), "\n\n")
	if !found {
		return c, errors.New("gitobj: commit has no blank line before message")
	}
	haveTree, haveAuthor, haveCommitter := false, false, false
	for _, line := range strings.Split(head, "\n") {
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			return c, fmt.Errorf("gitobj: malformed commit header %q", line)
		}
		switch key {
		case "tree":
			if haveTree || !ValidOID(val) {
				return c, fmt.Errorf("gitobj: invalid tree header %q", val)
			}
			c.Tree, haveTree = val, true
		case "parent":
			if !ValidOID(val) {
				return c, fmt.Errorf("gitobj: invalid parent oid %q", val)
			}
			c.Parents = append(c.Parents, val)
		case "author", "committer":
			sig, err := parseSignature(val)
			if err != nil {
				return c, err
			}
			if key == "author" {
				c.Author, haveAuthor = sig, true
			} else {
				c.Committer, haveCommitter = sig, true
			}
		}
	}
	if !haveTree {
		return c, errors.New("gitobj: commit has no tree")
	}
	if !haveAuthor || !haveCommitter {
		return c, errors.New("gitobj: commit has no author or committer")
	}
	c.Message = msg
	return c, nil
}

func decodeOID(s string) ([]byte, error) {
	if !ValidOID(s) {
		return nil, fmt.Errorf("gitobj: invalid oid %q", s)
	}
	return hex.DecodeString(s)
}

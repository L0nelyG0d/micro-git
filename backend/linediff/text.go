// Package linediff computes line-based (LCS) diffs between two texts and
// between two git trees, in the shape of plan.md "API contracts > commit
// detail". It uses only the standard library and the object store defined in
// package treebuild.
package linediff

import "strings"

// ContextLines is the number of unchanged lines kept around each change.
const ContextLines = 3

// MaxLines is the largest number of lines per side that is diffed. Files with
// more lines on either side are reported as truncated. It bounds the LCS table
// to MaxLines*MaxLines uint16 cells.
const MaxLines = 5000

// Line types.
const (
	Context = "context"
	Add     = "add"
	Del     = "del"
)

// Line is one line of a hunk. Text has no +/- prefix and no newline.
type Line struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Hunk is a run of changes with surrounding context. Starts are 1-based; a
// side with zero lines has start 0.
type Hunk struct {
	OldStart int    `json:"old_start"`
	OldLines int    `json:"old_lines"`
	NewStart int    `json:"new_start"`
	NewLines int    `json:"new_lines"`
	Lines    []Line `json:"lines"`
}

// SplitLines splits text on "\n". A final newline does not produce an extra
// empty line, so "a" and "a\n" both give ["a"]; "" gives no lines.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// Result is the diff of two texts.
type Result struct {
	Hunks     []Hunk // never nil
	Additions int
	Deletions int
	// Truncated is true when either side has more than MaxLines lines; then
	// Hunks is empty and the counts are zero.
	Truncated bool
}

// Diff compares old and new line by line.
func Diff(old, new string) Result {
	a, b := SplitLines(old), SplitLines(new)
	if len(a) > MaxLines || len(b) > MaxLines {
		return Result{Hunks: []Hunk{}, Truncated: true}
	}
	return diffLines(a, b)
}

type op struct {
	typ  string
	text string
}

func diffLines(a, b []string) Result {
	ops := script(a, b)
	res := Result{Hunks: buildHunks(ops)}
	for _, o := range ops {
		switch o.typ {
		case Add:
			res.Additions++
		case Del:
			res.Deletions++
		}
	}
	return res
}

// script returns the full edit script (every line of both sides) from an LCS.
func script(a, b []string) []op {
	// Trim common prefix and suffix to keep the table small.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]

	ops := make([]op, 0, len(a)+len(b))
	for _, s := range a[:pre] {
		ops = append(ops, op{Context, s})
	}
	ops = append(ops, lcsScript(ma, mb)...)
	for _, s := range a[len(a)-suf:] {
		ops = append(ops, op{Context, s})
	}
	return ops
}

// lcsScript builds the classic DP table (suffix LCS lengths) in one flat
// uint16 slice and walks it, preferring deletions before additions.
func lcsScript(a, b []string) []op {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		ops := make([]op, 0, n+m)
		for _, s := range a {
			ops = append(ops, op{Del, s})
		}
		for _, s := range b {
			ops = append(ops, op{Add, s})
		}
		return ops
	}
	w := m + 1
	t := make([]uint16, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i*w+j] = t[(i+1)*w+j+1] + 1
			} else if t[(i+1)*w+j] >= t[i*w+j+1] {
				t[i*w+j] = t[(i+1)*w+j]
			} else {
				t[i*w+j] = t[i*w+j+1]
			}
		}
	}
	ops := make([]op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{Context, a[i]})
			i++
			j++
		case t[(i+1)*w+j] >= t[i*w+j+1]:
			ops = append(ops, op{Del, a[i]})
			i++
		default:
			ops = append(ops, op{Add, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{Del, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{Add, b[j]})
	}
	return ops
}

// buildHunks groups an edit script into hunks with ContextLines of context.
// Changes whose context overlaps or touches end up in the same hunk.
func buildHunks(ops []op) []Hunk {
	hunks := []Hunk{}
	// Positions (0-based, in ops) of the first and last op of each hunk.
	type span struct{ lo, hi int }
	var spans []span
	for i, o := range ops {
		if o.typ == Context {
			continue
		}
		lo, hi := i-ContextLines, i+ContextLines
		if lo < 0 {
			lo = 0
		}
		if hi > len(ops)-1 {
			hi = len(ops) - 1
		}
		if k := len(spans) - 1; k >= 0 && lo <= spans[k].hi+1 {
			spans[k].hi = hi
		} else {
			spans = append(spans, span{lo, hi})
		}
	}
	// Line numbers before each op.
	oldNo, newNo := make([]int, len(ops)+1), make([]int, len(ops)+1)
	for i, o := range ops {
		oldNo[i+1], newNo[i+1] = oldNo[i], newNo[i]
		if o.typ != Add {
			oldNo[i+1]++
		}
		if o.typ != Del {
			newNo[i+1]++
		}
	}
	for _, s := range spans {
		h := Hunk{Lines: make([]Line, 0, s.hi-s.lo+1)}
		for _, o := range ops[s.lo : s.hi+1] {
			h.Lines = append(h.Lines, Line{o.typ, o.text})
		}
		h.OldLines = oldNo[s.hi+1] - oldNo[s.lo]
		h.NewLines = newNo[s.hi+1] - newNo[s.lo]
		h.OldStart, h.NewStart = oldNo[s.lo]+1, newNo[s.lo]+1
		if h.OldLines == 0 {
			h.OldStart = 0
		}
		if h.NewLines == 0 {
			h.NewStart = 0
		}
		hunks = append(hunks, h)
	}
	return hunks
}

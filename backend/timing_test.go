package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// captureTimingLog redirects the standard logger into a buffer for one test.
func captureTimingLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestTimingLogsPatternNotPath(t *testing.T) {
	buf := captureTimingLog(t)
	h := testServer(t).Handler()
	req := httptest.NewRequest(http.MethodGet, "/repos/alice/secret-repo/tree/main?token=hunter2", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "cookie-value"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	out := buf.String()
	if !strings.Contains(out, "GET /repos/{owner}/{repo}/tree/{ref} ") {
		t.Errorf("no route pattern in %q", out)
	}
	for _, leak := range []string{"alice", "secret-repo", "hunter2", "cookie-value", "token"} {
		if strings.Contains(out, leak) {
			t.Errorf("log leaks %q: %q", leak, out)
		}
	}
	if strings.Contains(out, "slow:") {
		t.Errorf("unexpected line %q", out)
	}
}

func TestTimingStatusAndSlow(t *testing.T) {
	buf := captureTimingLog(t)
	h := timing(http.NewServeMux(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(slowRequest + 20*time.Millisecond)
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("secret body")))
	out := buf.String()
	if !strings.Contains(out, "slow: POST - 418 ") {
		t.Errorf("line = %q", out)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("body leaked: %q", out)
	}
}

var timingLine = regexp.MustCompile(`([0-9.]+)ms stmts=(\d+)\n`)

// lastTiming returns the duration and statement count of the last log line.
func lastTiming(t *testing.T, buf *bytes.Buffer) (ms float64, stmts int) {
	t.Helper()
	m := timingLine.FindAllStringSubmatch(buf.String(), -1)
	if len(m) == 0 {
		t.Fatalf("no timing line in %q", buf.String())
	}
	ms, _ = strconv.ParseFloat(m[len(m)-1][1], 64)
	stmts, _ = strconv.Atoi(m[len(m)-1][2])
	return ms, stmts
}

func TestTimingCountsStatements(t *testing.T) {
	s := testServer(t)
	buf := captureTimingLog(t)
	rec := do(s, http.MethodGet, "/repos?per_page=1", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, n := lastTiming(t, buf); n < 1 {
		t.Errorf("stmts = %d, want >= 1; log %q", n, buf)
	}
	if !strings.Contains(buf.String(), "GET /repos ") {
		t.Errorf("log %q", buf)
	}
}

// TestBaseline prints the numbers recorded in _docs/performance.md. It only
// runs when BASELINE=1 and is not an assertion about speed.
//
//	BASELINE=1 go test -run TestBaseline -v .
func TestBaseline(t *testing.T) {
	if os.Getenv("BASELINE") == "" {
		t.Skip("set BASELINE=1 to measure")
	}
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"base"}`)
	base := "/repos/" + owner + "/base"
	buf := captureTimingLog(t)

	// Fixture: a root file, a file 3 levels deep, and a 50-commit main line.
	tip := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "init",
		up("README.md", "hello\n"), up("a/b/c/deep.txt", "deep\n"), up("a/b/x.txt", "x"))).OID
	root := tip
	for i := 0; i < 49; i++ {
		tip = mustCommit(t, s, owner, c, repo, commitJSON("main", tip, fmt.Sprint("m", i), up(fmt.Sprint("m", i, ".txt"), "m"))).OID
	}

	const runs = 7
	type row struct {
		name  string
		ms    float64
		stmts int
	}
	var rows []row
	measure := func(name string, f func(i int) *httptest.ResponseRecorder) {
		var times []float64
		stmts := 0
		for i := 0; i < runs+1; i++ { // first run is a warm-up
			buf.Reset()
			if rec := f(i); rec.Code >= 300 {
				t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
			}
			ms, n := lastTiming(t, buf)
			if i > 0 {
				times = append(times, ms)
			}
			stmts = n
		}
		sort.Float64s(times)
		rows = append(rows, row{name, times[len(times)/2], stmts})
	}
	get := func(path string) func(int) *httptest.ResponseRecorder {
		return func(int) *httptest.ResponseRecorder { return do(s, http.MethodGet, base+path, "", nil) }
	}
	measure("GET tree (root)", get("/tree/main"))
	measure("GET tree (3 levels deep)", get("/tree/main/a/b/c"))
	measure("GET blob", get("/blob/main/a/b/c/deep.txt"))
	measure("GET commits", get("/commits?ref=main"))
	measure("GET commit detail", get("/commits/"+tip))
	measure("POST merge (50-commit history)", func(i int) *httptest.ResponseRecorder {
		name := fmt.Sprint("target", i)
		if err := NewRepoStore(s.DB, repo.ID).CreateRef(context.Background(), name, root); err != nil {
			t.Fatal(err)
		}
		buf.Reset()
		return do(s, http.MethodPost, base+"/merge", mergeBody(name, "main"), c)
	})

	next := 0
	post := func(n int) func(int) *httptest.ResponseRecorder {
		return func(int) *httptest.ResponseRecorder {
			var ch []string
			for j := 0; j < n; j++ {
				next++
				ch = append(ch, up(fmt.Sprint("p", next, ".txt"), "p"))
			}
			rec := do(s, http.MethodPost, base+"/commits", commitJSON("main", tip, "p", ch...), c)
			if rec.Code == 201 {
				tip = mustCommitOID(rec.Body.Bytes())
			}
			return rec
		}
	}
	measure("POST commit (1 change)", post(1))
	measure("POST commit (10 changes)", post(10))
	measure("POST commit (10 changes, 3 folders)", func(int) *httptest.ResponseRecorder {
		var ch []string
		for j := 0; j < 10; j++ {
			next++
			ch = append(ch, up(fmt.Sprintf("%c/q%d.txt", 'a'+j%3, next), "p"))
		}
		rec := do(s, http.MethodPost, base+"/commits", commitJSON("main", tip, "p", ch...), c)
		if rec.Code == 201 {
			tip = mustCommitOID(rec.Body.Bytes())
		}
		return rec
	})
	fmt.Println("| Request | Statements | Median ms |")
	fmt.Println("|---|---|---|")
	for _, r := range rows {
		fmt.Printf("| %s | %d | %.1f |\n", r.name, r.stmts, r.ms)
	}
}

func mustCommitOID(body []byte) string {
	m := regexp.MustCompile(`"oid":"([0-9a-f]+)"`).FindSubmatch(body)
	if m == nil {
		return ""
	}
	return string(m[1])
}

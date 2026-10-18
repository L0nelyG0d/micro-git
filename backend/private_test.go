package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// A private repository answers 404 to everyone but its owner, on every read
// endpoint, and is absent from the public list.
func TestPrivateRepoVisibility(t *testing.T) {
	s := testServer(t)
	owner, oc := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	repo := createRepo(t, s, oc, `{"name":"secret","private":true}`)
	if !repo.Private {
		t.Fatalf("created repo is not private: %+v", repo)
	}
	mustCommit(t, s, owner, oc, repo, commitJSON("main", "", "first", up("a.txt", "hello")))
	base := "/repos/" + owner + "/secret"

	for _, path := range []string{"", "/commits", "/branches", "/tree/main", "/blob/main/a.txt"} {
		if rec := do(s, http.MethodGet, base+path, "", oc); rec.Code != http.StatusOK {
			t.Errorf("owner GET %q: %d %s", path, rec.Code, rec.Body)
		}
		for name, c := range map[string]*http.Cookie{"anonymous": nil, "other user": other} {
			rec := do(s, http.MethodGet, base+path, "", c)
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "repository not found") {
				t.Errorf("%s GET %q: %d %s", name, path, rec.Code, rec.Body)
			}
		}
	}

	listed := func(c *http.Cookie) bool {
		rec := do(s, http.MethodGet, "/repos?owner="+owner, "", c)
		for _, r := range decodeRepos(t, rec.Body) {
			if r.Name == "secret" {
				return true
			}
		}
		return false
	}
	if !listed(oc) {
		t.Error("owner does not see the private repo in the list")
	}
	if listed(nil) || listed(other) {
		t.Error("private repo leaked into the list")
	}
}

// The owner can flip visibility with PATCH; reads follow.
func TestPrivateRepoToggle(t *testing.T) {
	s := testServer(t)
	owner, oc := newUserCookie(t, s)
	createRepo(t, s, oc, `{"name":"flip"}`)
	url := "/repos/" + owner + "/flip"

	if rec := do(s, http.MethodGet, url, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("public repo not readable anonymously: %d", rec.Code)
	}
	if rec := do(s, http.MethodPatch, url, `{"private":true}`, oc); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"private":true`) {
		t.Fatalf("make private: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s, http.MethodGet, url, "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("private repo readable anonymously: %d", rec.Code)
	}
	if rec := do(s, http.MethodPatch, url, `{"private":false}`, oc); rec.Code != http.StatusOK {
		t.Fatalf("make public: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s, http.MethodGet, url, "", nil); rec.Code != http.StatusOK {
		t.Errorf("public again, anonymous: %d", rec.Code)
	}
}

// Responses of a private repo must not be stored by shared caches: oid
// addressed ones are "private, ... immutable", branch addressed ones are
// "private, no-cache" with an ETag. A public repo keeps the shared forms.
func TestPrivateRepoCacheControl(t *testing.T) {
	s := testServer(t)
	owner, oc := newUserCookie(t, s)
	priv := createRepo(t, s, oc, `{"name":"cc","private":true}`)
	pub := createRepo(t, s, oc, `{"name":"ccpub"}`)
	pc := mustCommit(t, s, owner, oc, priv, commitJSON("main", "", "first", up("a.txt", "hello")))
	mustCommit(t, s, owner, oc, pub, commitJSON("main", "", "first", up("a.txt", "hello")))

	for _, tc := range []struct{ name, path, want string }{
		{"private tree by oid", "/cc/tree/" + pc.OID, "private, max-age=31536000, immutable"},
		{"private tree by branch", "/cc/tree/main", "private, no-cache"},
		{"private blob by branch", "/cc/blob/main/a.txt", "private, no-cache"},
		{"public tree by branch", "/ccpub/tree/main", "no-cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(s, http.MethodGet, "/repos/"+owner+tc.path, "", oc)
			if rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.want {
				t.Errorf("Cache-Control = %q, want %q", got, tc.want)
			}
			if strings.Contains(tc.path, "main") && rec.Header().Get("ETag") == "" {
				t.Error("branch-addressed response has no ETag")
			}
		})
	}
}

// privateFixture is a private repo with two commits on main and a branch, plus
// a public repo, owned by one user; other is a second signed-in user.
type privateFixture struct {
	s            *Server
	owner        string
	oc, other    *http.Cookie
	first, tip   commitResp
	base, pubURL string
}

func newPrivateFixture(t *testing.T) privateFixture {
	t.Helper()
	s := testServer(t)
	owner, oc := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	repo := createRepo(t, s, oc, `{"name":"sec","private":true}`)
	pub := createRepo(t, s, oc, `{"name":"open"}`)
	f := privateFixture{s: s, owner: owner, oc: oc, other: other,
		base: "/repos/" + owner + "/sec", pubURL: "/repos/" + owner + "/open"}
	f.first = mustCommit(t, s, owner, oc, repo, commitJSON("main", "", "first", up("a.txt", "one\n")))
	f.tip = mustCommit(t, s, owner, oc, repo, commitJSON("main", f.first.OID, "second", up("a.txt", "two\n")))
	mustCommit(t, s, owner, oc, pub, commitJSON("main", "", "first", up("a.txt", "one\n")))
	for _, base := range []string{f.base, f.pubURL} {
		if rec := do(s, http.MethodPost, base+"/branches", `{"name":"feat","from":"main"}`, oc); rec.Code != http.StatusCreated {
			t.Fatalf("branch: %d %s", rec.Code, rec.Body)
		}
	}
	return f
}

// Every other read shape of a private repo is 404 for non-owners, while the
// owner still reads it. (Pins existing behavior.)
func TestPrivateRepoReadShapes(t *testing.T) {
	f := newPrivateFixture(t)
	for _, tc := range []struct{ name, path string }{
		{"commit by oid", "/commits/" + f.tip.OID},
		{"compare", "/compare?base=main&head=feat"},
		{"commits ?ref=", "/commits?ref=feat"},
		{"tree by oid", "/tree/" + f.tip.OID},
		{"blob by oid", "/blob/" + f.tip.OID + "/a.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := do(f.s, http.MethodGet, f.base+tc.path, "", f.oc); rec.Code != http.StatusOK {
				t.Fatalf("owner: %d %s", rec.Code, rec.Body)
			}
			for who, c := range map[string]*http.Cookie{"anonymous": nil, "non-owner": f.other} {
				if rec := do(f.s, http.MethodGet, f.base+tc.path, "", c); rec.Code != http.StatusNotFound {
					t.Errorf("%s: %d %s", who, rec.Code, rec.Body)
				}
			}
		})
	}
}

// A conditional GET must not reveal a private repo: an anonymous client with
// the owner's ETag gets 404, never 304.
func TestPrivateRepoConditionalGet(t *testing.T) {
	f := newPrivateFixture(t)
	for _, path := range []string{"/tree/main", "/blob/main/a.txt"} {
		rec := do(f.s, http.MethodGet, f.base+path, "", f.oc)
		etag := rec.Header().Get("ETag")
		if rec.Code != http.StatusOK || etag == "" {
			t.Fatalf("owner %s: %d etag=%q", path, rec.Code, etag)
		}
		for who, c := range map[string]*http.Cookie{"anonymous": nil, "non-owner": f.other} {
			req := httptest.NewRequest(http.MethodGet, f.base+path, nil)
			req.Header.Set("If-None-Match", etag)
			if c != nil {
				req.AddCookie(c)
			}
			got := httptest.NewRecorder()
			f.s.Handler().ServeHTTP(got, req)
			if got.Code != http.StatusNotFound {
				t.Errorf("%s %s with If-None-Match: %d, want 404", who, path, got.Code)
			}
		}
		// the owner still gets the 304
		req := httptest.NewRequest(http.MethodGet, f.base+path, nil)
		req.Header.Set("If-None-Match", etag)
		req.AddCookie(f.oc)
		got := httptest.NewRecorder()
		f.s.Handler().ServeHTTP(got, req)
		if got.Code != http.StatusNotModified {
			t.Errorf("owner %s with If-None-Match: %d, want 304", path, got.Code)
		}
	}
}

// Writes by someone who is not the owner: 404 on a private repo (it must not
// be revealed), 403 on a public one.
func TestPrivateRepoWritesByNonOwner(t *testing.T) {
	f := newPrivateFixture(t)
	commit := commitJSON("main", f.tip.OID, "x", up("b.txt", "b"))
	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"commit", http.MethodPost, "/commits", commit},
		{"create branch", http.MethodPost, "/branches", `{"name":"nb","from":"main"}`},
		{"delete branch", http.MethodDelete, "/branches/feat", ""},
		{"merge", http.MethodPost, "/merge", `{"base":"main","head":"feat"}`},
		{"patch", http.MethodPatch, "", `{"private":false}`},
		{"delete repo", http.MethodDelete, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range []struct {
				who    string
				url    string
				cookie *http.Cookie
				want   int
			}{
				{"private non-owner", f.base, f.other, http.StatusNotFound},
				{"public non-owner", f.pubURL, f.other, http.StatusForbidden},
			} {
				body := tc.body
				if tc.name == "commit" && c.url == f.pubURL {
					body = commitJSON("main", "", "x", up("b.txt", "b"))
				}
				if rec := do(f.s, tc.method, c.url+tc.path, body, c.cookie); rec.Code != c.want {
					t.Errorf("%s: %d %s, want %d", c.who, rec.Code, rec.Body, c.want)
				}
			}
		})
	}
	// nothing changed
	if rec := do(f.s, http.MethodGet, f.base, "", f.oc); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"private":true`) {
		t.Errorf("private repo changed: %d %s", rec.Code, rec.Body)
	}
}

// Pagination never leaks private repos to others, and has_more ignores them.
func TestPrivateRepoPaginationHides(t *testing.T) {
	s := testServer(t)
	owner, oc := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	for _, b := range []string{
		`{"name":"p1"}`, `{"name":"s1","private":true}`, `{"name":"p2"}`,
		`{"name":"s2","private":true}`, `{"name":"p3"}`,
	} {
		createRepo(t, s, oc, b)
	}
	names := func(c *http.Cookie, q string) (out []string, more bool) {
		rec := do(s, http.MethodGet, "/repos?owner="+owner+q, "", c)
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", q, rec.Code, rec.Body)
		}
		var body struct {
			HasMore bool `json:"has_more"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, r := range decodeRepos(t, rec.Body) {
			out = append(out, r.Name)
		}
		return out, body.HasMore
	}
	for who, c := range map[string]*http.Cookie{"anonymous": nil, "non-owner": other} {
		var all []string
		for page := 1; page <= 2; page++ {
			got, more := names(c, "&per_page=2&page="+strconv.Itoa(page))
			all = append(all, got...)
			if want := page == 1; more != want {
				t.Errorf("%s page %d has_more = %v, want %v", who, page, more, want)
			}
		}
		if strings.Join(all, ",") != "p3,p2,p1" && strings.Join(all, ",") != "p1,p2,p3" {
			t.Errorf("%s saw %v, want the 3 public repos", who, all)
		}
	}
	if got, _ := names(oc, "&per_page=10"); len(got) != 5 {
		t.Errorf("owner sees %v, want 5", got)
	}
}

// The owner can flip private -> public and everyone can then read it.
func TestPrivateRepoOwnerPatchToPublic(t *testing.T) {
	f := newPrivateFixture(t)
	rec := do(f.s, http.MethodPatch, f.base, `{"private":false}`, f.oc)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"private":false`) {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"", "/tree/main", "/commits/" + f.tip.OID} {
		if rec := do(f.s, http.MethodGet, f.base+path, "", nil); rec.Code != http.StatusOK {
			t.Errorf("anonymous GET %q after public: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := do(f.s, http.MethodGet, f.base+"/tree/main", "", nil); rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control after public = %q", rec.Header().Get("Cache-Control"))
	}
}

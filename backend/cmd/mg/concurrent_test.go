package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"content":%q}`, strings.Repeat("a", maxResponseBytes+10))
	}))
	defer srv.Close()
	var out map[string]string
	_, err := config{Server: srv.URL}.call("GET", "/x", nil, &out)
	if err == nil || !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("err = %v, want response too large", err)
	}
}

func TestCallAcceptsResponseAtLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"content":%q}`, strings.Repeat("a", 2<<20))
	}))
	defer srv.Close()
	var out map[string]string
	if _, err := (config{Server: srv.URL}).call("GET", "/x", nil, &out); err != nil || len(out["content"]) != 2<<20 {
		t.Fatalf("err = %v, len = %d", err, len(out["content"]))
	}
}

// bulkRemote serves n files f000..; failOn (if >=0) makes that blob answer 500.
// It records the peak number of concurrent blob requests.
func bulkRemote(t *testing.T, n, failOn int) (cfg config, maxInFlight *int32, blobHits *int32) {
	t.Helper()
	var inflight, peak, hits int32
	mux := http.NewServeMux()
	wj := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/repos/o/n", func(w http.ResponseWriter, r *http.Request) {
		wj(w, map[string]string{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/o/n/branches", func(w http.ResponseWriter, r *http.Request) {
		wj(w, map[string]any{"branches": []map[string]string{{"name": "main", "oid": "newtip1234"}}})
	})
	mux.HandleFunc("/repos/o/n/tree/", func(w http.ResponseWriter, r *http.Request) {
		var es []map[string]string
		for i := 0; i < n; i++ {
			es = append(es, map[string]string{"name": fmt.Sprintf("f%03d", i), "type": "blob"})
		}
		wj(w, map[string]any{"entries": es})
	})
	mux.HandleFunc("/repos/o/n/blob/", func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inflight, 1)
		defer atomic.AddInt32(&inflight, -1)
		atomic.AddInt32(&hits, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if failOn >= 0 && name == fmt.Sprintf("f%03d", failOn) {
			w.WriteHeader(500)
			wj(w, map[string]string{"error": "boom"})
			return
		}
		wj(w, map[string]string{"content": "content of " + name + "\n"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return config{Server: srv.URL}, &peak, &hits
}

func TestCloneFetchesBlobsConcurrently(t *testing.T) {
	cfg, peak, _ := bulkRemote(t, 200, -1)
	dir := filepath.Join(t.TempDir(), "wc")
	msg, err := doClone(cfg, "o/n", dir)
	if err != nil {
		t.Fatal(err)
	}
	if msg != fmt.Sprintf("cloned o/n:main into %s (200 files)", dir) {
		t.Fatalf("message = %q", msg)
	}
	if p := atomic.LoadInt32(peak); p < 2 || p > blobWorkers {
		t.Fatalf("peak in-flight = %d, want 2..%d", p, blobWorkers)
	}
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("f%03d", i)
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != "content of "+name+"\n" {
			t.Fatalf("%s = %q, %v", name, b, err)
		}
	}
	st, err := loadState(dir)
	if err != nil || len(st.Files) != 200 || st.Tip != "newtip1234" {
		t.Fatalf("state = %+v, %v", st, err)
	}
}

func TestCloneFailingBlobWritesNoState(t *testing.T) {
	cfg, _, _ := bulkRemote(t, 50, 7)
	dir := filepath.Join(t.TempDir(), "wc")
	_, err := doClone(cfg, "o/n", dir)
	if err == nil || !strings.Contains(err.Error(), "cannot read f007") {
		t.Fatalf("err = %v, want cannot read f007", err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateDir, "state.json")); err == nil {
		t.Fatal("state.json written after a failed clone")
	}
}

func TestCloneReportsFirstErrorInOrder(t *testing.T) {
	// Two blobs fail; whichever answers first, the lower path is reported.
	for i := 0; i < 5; i++ {
		cfg, _, _ := bulkRemote(t, 40, 3)
		_, err := doClone(cfg, "o/n", filepath.Join(t.TempDir(), "wc"))
		if err == nil || !strings.Contains(err.Error(), "cannot read f003") {
			t.Fatalf("run %d: err = %v", i, err)
		}
	}
}

func TestPullFailingBlobKeepsState(t *testing.T) {
	cfg, _, _ := bulkRemote(t, 30, 5)
	dir, _ := newWorkingCopy(t)
	// compare is not served by bulkRemote's mux, so use the empty-repo path.
	st, _ := loadState(dir)
	st.Tip = ""
	st.Repo = "o/n"
	if err := st.save(dir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, stateDir, "state.json"))
	if _, err := doPull(cfg, dir, true); err == nil || !strings.Contains(err.Error(), "cannot read f005") {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, stateDir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("state.json changed after a failed pull")
	}
}

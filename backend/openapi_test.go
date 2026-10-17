package main

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The OpenAPI contract (../openapi.yaml) must list exactly the routes that
// handler.go registers. Both files are scanned line by line with the standard
// library only; no database is needed.

var (
	handleFuncRE = regexp.MustCompile(`mux\.HandleFunc\(([^,]+),\s*(.*)$`)
	quotedRE     = regexp.MustCompile(`"([^"]*)"`)
	methodKeyRE  = regexp.MustCompile(`http\.Method(\w+)\b`)
	loopKindRE   = regexp.MustCompile(`\{"(\w+)",\s*s\.\w+\}`)
	loopBaseRE   = regexp.MustCompile(`base := "([^"]*)" \+ kind\.name \+ "([^"]*)"`)
)

// routeKey is "METHOD /path" with {x...} written as {x}.
func routeKey(method, path string) string {
	path = regexp.MustCompile(`\{(\w+)\.\.\.\}`).ReplaceAllString(path, "{$1}")
	return strings.ToUpper(method) + " " + path
}

// scanHandlerRoutes reads the route registrations of Server.routes: plain
// mux.HandleFunc calls with allow(...) or a methods{...} literal, and the
// tree/blob loop. A bare handler function (health) serves GET and HEAD.
func scanHandlerRoutes(src string) []string {
	var routes []string
	lines := strings.Split(src, "\n")
	var kinds []string
	var loopMethods []string
	var loopPrefix, loopSuffix string
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if m := loopKindRE.FindAllStringSubmatch(line, -1); m != nil {
			for _, k := range m {
				kinds = append(kinds, k[1])
			}
		}
		if m := loopBaseRE.FindStringSubmatch(line); m != nil {
			loopPrefix, loopSuffix = m[1], m[2]
		}
		if strings.HasPrefix(line, "h := methods{") {
			for _, m := range methodKeyRE.FindAllStringSubmatch(line, -1) {
				loopMethods = append(loopMethods, m[1])
			}
		}
		m := handleFuncRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pathExpr, rest := strings.TrimSpace(m[1]), m[2]
		if strings.HasPrefix(pathExpr, "base") { // tree/blob loop
			suffix := ""
			if q := quotedRE.FindStringSubmatch(pathExpr); q != nil {
				suffix = q[1]
			}
			for _, kind := range kinds {
				for _, method := range loopMethods {
					routes = append(routes, routeKey(method, loopPrefix+kind+loopSuffix+suffix))
				}
			}
			continue
		}
		q := quotedRE.FindStringSubmatch(pathExpr)
		if q == nil || q[1] == "/" { // "/" is the catch-all 404
			continue
		}
		path := q[1]
		var methodsOfRoute []string
		switch {
		case strings.HasPrefix(rest, "allow("):
			methodsOfRoute = append(methodsOfRoute, methodKeyRE.FindStringSubmatch(rest)[1])
		case strings.HasPrefix(rest, "methods{"):
			for j := i + 1; j < len(lines) && !strings.Contains(lines[j-1], "}.serve)"); j++ {
				if mm := methodKeyRE.FindStringSubmatch(lines[j]); mm != nil && strings.Contains(lines[j], ":") {
					methodsOfRoute = append(methodsOfRoute, mm[1])
				}
			}
		default:
			methodsOfRoute = []string{"Get", "Head"}
		}
		for _, method := range methodsOfRoute {
			routes = append(routes, routeKey(method, path))
		}
	}
	sort.Strings(routes)
	return routes
}

var specMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true}

// scanSpecRoutes reads the paths: section of an OpenAPI file: a path is a line
// indented by two spaces that starts with "/", a method is a line indented by
// four spaces.
func scanSpecRoutes(src string) []string {
	var routes []string
	inPaths := false
	path := ""
	for _, line := range strings.Split(src, "\n") {
		if line != "" && line[0] != ' ' && line[0] != '#' {
			inPaths = strings.HasPrefix(line, "paths:")
			continue
		}
		if !inPaths {
			continue
		}
		switch {
		case strings.HasPrefix(line, "  /"):
			path = strings.TrimSuffix(strings.TrimSpace(line), ":")
		case strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") && path != "":
			name := strings.TrimSuffix(strings.TrimSpace(line), ":")
			if specMethods[name] && strings.HasSuffix(strings.TrimSpace(line), ":") {
				routes = append(routes, routeKey(name, path))
			}
		}
	}
	sort.Strings(routes)
	return routes
}

// diffRoutes returns what is only in a and what is only in b.
func diffRoutes(a, b []string) (onlyA, onlyB []string) {
	in := func(list []string) map[string]bool {
		m := map[string]bool{}
		for _, s := range list {
			m[s] = true
		}
		return m
	}
	ma, mb := in(a), in(b)
	for _, s := range a {
		if !mb[s] {
			onlyA = append(onlyA, s)
		}
	}
	for _, s := range b {
		if !ma[s] {
			onlyB = append(onlyB, s)
		}
	}
	return onlyA, onlyB
}

func TestOpenAPIMatchesRoutes(t *testing.T) {
	handler, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	code, doc := scanHandlerRoutes(string(handler)), scanSpecRoutes(string(spec))
	if len(code) < 20 {
		t.Fatalf("scanner found only %d routes in handler.go: %v", len(code), code)
	}
	onlyCode, onlySpec := diffRoutes(code, doc)
	for _, r := range onlyCode {
		t.Errorf("route %q is registered in handler.go but missing from openapi.yaml", r)
	}
	for _, r := range onlySpec {
		t.Errorf("route %q is in openapi.yaml but not registered in handler.go", r)
	}
}

const sampleHandler = `
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/auth/login", allow(http.MethodPost, s.login))
	mux.HandleFunc("/repos/{owner}/{repo}", methods{
		http.MethodGet:    s.getRepo,
		http.MethodDelete: s.requireAuth(s.deleteRepo),
	}.serve)
	for _, kind := range []struct {
		name string
		get  http.HandlerFunc
	}{{"tree", s.getTree}, {"blob", s.getBlob}} {
		h := methods{http.MethodGet: kind.get}.serve
		base := "/repos/{owner}/{repo}/" + kind.name + "/{ref}"
		mux.HandleFunc(base, h)
		mux.HandleFunc(base+"/{path...}", h)
	}
	mux.HandleFunc("/", notFound)
`

const sampleSpec = `openapi: 3.0.3
paths:
  /health:
    get:
      summary: x
    head:
      summary: x
  /auth/login:
    post:
      responses: {}
  /repos/{owner}/{repo}:
    parameters: []
    get:
      x: y
    delete:
      x: y
  /repos/{owner}/{repo}/tree/{ref}:
    get:
      x: y
  /repos/{owner}/{repo}/tree/{ref}/{path}:
    get:
      x: y
  /repos/{owner}/{repo}/blob/{ref}:
    get:
      x: y
  /repos/{owner}/{repo}/blob/{ref}/{path}:
    get:
      x: y
components:
  schemas:
    Thing:
      get: not a route
`

func TestRouteScanners(t *testing.T) {
	want := []string{
		"DELETE /repos/{owner}/{repo}",
		"GET /health",
		"GET /repos/{owner}/{repo}",
		"GET /repos/{owner}/{repo}/blob/{ref}",
		"GET /repos/{owner}/{repo}/blob/{ref}/{path}",
		"GET /repos/{owner}/{repo}/tree/{ref}",
		"GET /repos/{owner}/{repo}/tree/{ref}/{path}",
		"HEAD /health",
		"POST /auth/login",
	}
	code, doc := scanHandlerRoutes(sampleHandler), scanSpecRoutes(sampleSpec)
	if !reflect.DeepEqual(code, want) {
		t.Errorf("handler scan = %v, want %v", code, want)
	}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("spec scan = %v, want %v", doc, want)
	}
	if a, b := diffRoutes(code, doc); len(a)+len(b) != 0 {
		t.Errorf("samples differ: %v / %v", a, b)
	}

	// A route missing from the spec, and one that only the spec has, are reported.
	trimmed := strings.Replace(sampleSpec, "  /auth/login:\n    post:\n      responses: {}\n", "", 1)
	extra := strings.Replace(trimmed, "paths:\n", "paths:\n  /gone:\n    get:\n      x: y\n", 1)
	onlyCode, onlySpec := diffRoutes(scanHandlerRoutes(sampleHandler), scanSpecRoutes(extra))
	if !reflect.DeepEqual(onlyCode, []string{"POST /auth/login"}) {
		t.Errorf("only in code = %v", onlyCode)
	}
	if !reflect.DeepEqual(onlySpec, []string{"GET /gone"}) {
		t.Errorf("only in spec = %v", onlySpec)
	}
}

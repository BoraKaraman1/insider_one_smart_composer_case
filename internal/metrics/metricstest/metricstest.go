// Package metricstest scrapes a /metrics handler and reads the Prometheus
// text format, so tests check what a scraper sees without importing the
// client library. It is built only with the spec tag until stream 3 lands.
package metricstest

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Samples maps a series key, name{k="v",...} with labels sorted, to its
// value.
type Samples map[string]float64

var line = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})? (\S+)(?: \d+)?$`)
var label = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)

// Scrape GETs h with no Authorization header and parses the answer.
func Scrape(t testing.TB, h http.Handler) Samples {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics: %d %s", rec.Code, rec.Body)
	}
	return Parse(t, rec.Body.String())
}

// Parse reads the text exposition format.
func Parse(t testing.TB, text string) Samples {
	t.Helper()
	out := Samples{}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		m := line.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("not a sample line: %q", l)
		}
		v, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			t.Fatalf("sample %q: %v", l, err)
		}
		var labels []string
		for _, lm := range label.FindAllStringSubmatch(m[2], -1) {
			labels = append(labels, lm[1]+"="+lm[2])
		}
		out[Key(m[1], labels...)] = v
	}
	return out
}

// Key builds a series key from a name and "k=v" labels in any order.
func Key(name string, labels ...string) string {
	if len(labels) == 0 {
		return name
	}
	s := slices.Clone(labels)
	slices.Sort(s)
	parts := make([]string, len(s))
	for i, kv := range s {
		k, v, _ := strings.Cut(kv, "=")
		parts[i] = k + `="` + v + `"`
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

// Get returns a series value, 0 when it is absent (a counter not yet
// incremented is not exposed).
func (s Samples) Get(name string, labels ...string) float64 { return s[Key(name, labels...)] }

// Has reports whether a series is exposed.
func (s Samples) Has(name string, labels ...string) bool {
	_, ok := s[Key(name, labels...)]
	return ok
}

// Delta returns after minus before for one series.
func Delta(before, after Samples, name string, labels ...string) float64 {
	return after.Get(name, labels...) - before.Get(name, labels...)
}

// LabelValues returns every label value of every series.
func (s Samples) LabelValues() []string {
	var out []string
	for k := range s {
		for _, lm := range label.FindAllStringSubmatch(k, -1) {
			out = append(out, lm[2])
		}
	}
	return out
}

// Names returns the metric names of every series, histogram suffixes
// included.
func (s Samples) Names() []string {
	seen := map[string]bool{}
	for k := range s {
		name, _, _ := strings.Cut(k, "{")
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// idShaped matches UUIDs and long digit runs: campaign, message and user
// ids must never be label values.
var idShaped = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-|^\d{5,}$`)

// IDShaped reports whether a label value looks like an id.
func IDShaped(v string) bool { return idShaped.MatchString(v) }

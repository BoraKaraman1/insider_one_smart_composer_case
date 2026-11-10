package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The golden briefs run end to end against the mock's demo brain, and the
// report is written as markdown and as JSON with the header of plan 3.
func TestGoldenRunWritesReport(t *testing.T) {
	admin, tmpl := adminURL(t)
	before := evalDatabases(t, admin)
	cases, err := LoadCases("../../evals/briefs.json")
	if err != nil {
		t.Fatal(err)
	}
	cl, _ := mockClient(t)
	rep, err := Run(context.Background(), Config{AdminURL: admin, Template: tmpl, Cases: cases, Runs: 2, LLM: cl, Mode: "mock",
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Numbers.Generations != 2*len(cases) || len(rep.Cases) != 2*len(cases) {
		t.Fatalf("%d generations, %d results for %d cases and 2 runs", rep.Numbers.Generations, len(rep.Cases), len(cases))
	}
	if rep.Cases[len(cases)].Run != 2 {
		t.Errorf("second run numbered %d", rep.Cases[len(cases)].Run)
	}
	// The demo brain reads the worked example right.
	if rep.Cases[0].Name != "istanbul_cart_abandoners" || !rep.Cases[0].Result.Correct {
		t.Errorf("worked example under the demo brain: %+v", rep.Cases[0])
	}

	dir := t.TempDir()
	md, js := filepath.Join(dir, "r", "report.md"), filepath.Join(dir, "report.json")
	for _, p := range []string{md, js} {
		if err := WriteReport(p, rep); err != nil {
			t.Fatal(err)
		}
	}
	text := read(t, md)
	for _, want := range []string{"| mode | mock |", "| model | " + rep.Model + " |",
		"| prompt_version segment | " + rep.PromptVersions["segment"] + " |",
		"| prompt_version content | " + rep.PromptVersions["content"] + " |", "istanbul_cart_abandoners"} {
		if !strings.Contains(text, want) {
			t.Errorf("markdown report has no %q", want)
		}
	}
	var back Report
	if err := json.Unmarshal([]byte(read(t, js)), &back); err != nil {
		t.Fatal(err)
	}
	if back.Mode != "mock" || back.Model != rep.Model || back.Numbers.Generations != rep.Numbers.Generations {
		t.Errorf("JSON report header %q %q %d", back.Mode, back.Model, back.Numbers.Generations)
	}
	// No recipient data in either report.
	if strings.Contains(text, "@") || strings.Contains(read(t, js), "@") {
		t.Error("a report carries an email address")
	}
	if after := evalDatabases(t, admin); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("eval databases left behind: before %v, after %v", before, after)
	}
}

func TestLanguage(t *testing.T) {
	for text, want := range map[string]string{
		"Merhaba {{first_name}}, sepetin seni bekliyor":            "tr",
		"Sepetindeki ürünler":                                      "tr",
		"Hello {{first_name}}, your cart is waiting for you":       "en",
		"{{campaign_url}}":                                         "",
		"Hi {{first_name}}, %20 indirim: {{campaign_url}} for you": "en",
	} {
		if got := language(text); got != want {
			t.Errorf("language(%q) = %q, want %q", text, got, want)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

package integration_test

import (
	"context"
	"os"
	"regexp"
	"testing"

	g2p "github.com/androiddrew/go-g2p"
	"github.com/androiddrew/go-g2p/fallback/neural"
	"github.com/androiddrew/go-ttsnorm/corpus"
	"go.yaml.in/yaml/v3"

	"github.com/androiddrew/kokoro-run/internal/textprep"
)

// TestNormalizeCorpus reads go-ttsnorm's 476-input corpus the way the server
// does (markdown and normalize on) and checks that every input:
//
//   - phonemizes completely, with no unresolved or truncated spans;
//   - keeps digits only where gokittentts's golden reading does; and
//   - has the phonemes of that golden reading, or is listed with a reason in
//     testdata/normalize_differences.yaml.
//
// It logs how many inputs match without textprep, as a baseline.
func TestNormalizeCorpus(t *testing.T) {
	library := os.Getenv("KOKORO_TEST_ORT")
	if library == "" {
		t.Skip("set KOKORO_TEST_ORT for the normalization corpus")
	}
	fallback, err := neural.New(neural.Config{ORTLibrary: library})
	if err != nil {
		t.Fatal(err)
	}
	defer fallback.Close()
	frontend, err := g2p.New(g2p.Config{ORTLibrary: library, Fallback: fallback})
	if err != nil {
		t.Fatal(err)
	}
	defer frontend.Close() // runs before fallback.Close
	cases, err := corpus.Cases()
	if err != nil {
		t.Fatal(err)
	}
	differences := loadDifferences(t, cases)

	phonemize := func(text string) (string, bool) {
		r, err := frontend.Phonemize(context.Background(), g2p.Request{Text: text, Dialect: g2p.US, AllowTruncated: true})
		return r.Phonemes, err == nil && r.Complete && len(r.Diagnostics) == 0
	}
	digit := regexp.MustCompile(`\p{Nd}`)
	var matched, baseline, baselineComplete int
	for _, c := range cases {
		prepared := textprep.Text(c.Input, textprep.Options{Markdown: true, Normalize: true})
		got, complete := phonemize(prepared)
		want, _ := phonemize(c.Expected)
		raw, rawComplete := phonemize(c.Input)
		if rawComplete {
			baselineComplete++
		}
		if raw == want {
			baseline++
		}
		switch {
		case !complete:
			t.Errorf("%q: incomplete pronunciation of %q", c.Input, prepared)
		case digit.MatchString(prepared) && !digit.MatchString(c.Expected):
			t.Errorf("%q: digits left in %q; the golden reading is %q", c.Input, prepared, c.Expected)
		case got == want:
			matched++
			if why, listed := differences[c.Input]; listed {
				t.Errorf("%q: listed as differing (%s), but now matches the golden reading; remove it", c.Input, why)
			}
		default:
			if _, listed := differences[c.Input]; !listed {
				t.Errorf("%q: unreviewed difference from the golden reading %q\n prepared: %q\n      got: %s\n     want: %s", c.Input, c.Expected, prepared, got, want)
			}
		}
	}
	t.Logf("%d inputs: %d match the golden reading, %d are reviewed differences; without textprep %d match and %d are complete",
		len(cases), matched, len(differences), baseline, baselineComplete)
}

func loadDifferences(t *testing.T, cases []corpus.Case) map[string]string {
	t.Helper()
	data, err := os.ReadFile("../testdata/normalize_differences.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Input string `yaml:"input"`
		Why   string `yaml:"why"`
	}
	if err = yaml.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	inCorpus := map[string]bool{}
	for _, c := range cases {
		inCorpus[c.Input] = true
	}
	byInput := map[string]string{}
	for _, d := range list {
		switch {
		case !inCorpus[d.Input]:
			t.Errorf("normalize_differences.yaml: %q is not in the corpus", d.Input)
		case d.Why != "letters" && d.Why != "punctuation" && d.Why != "markdown":
			t.Errorf("normalize_differences.yaml: %q: why must be letters, punctuation or markdown", d.Input)
		case byInput[d.Input] != "":
			t.Errorf("normalize_differences.yaml: %q is listed twice", d.Input)
		}
		byInput[d.Input] = d.Why
	}
	return byInput
}

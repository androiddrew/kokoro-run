// Package textprep turns written text into text for the pronunciation
// frontend: markdown becomes prose, and numbers, dates, times, money, units,
// URLs and the like become words. Misaki's inline pronunciation overrides,
// such as [Kubernetes](/kˌubəɹnˈɛtiz/), pass through both steps unchanged.
package textprep

import (
	"github.com/androiddrew/go-ttsnorm/markdown"
	"github.com/androiddrew/go-ttsnorm/normalize"
)

// Options selects the steps.
type Options struct {
	Markdown  bool // read markdown as prose
	Normalize bool // read numbers, dates, times, URLs and the like as words
}

// Text applies the selected steps to s.
func Text(s string, o Options) string {
	if o.Markdown {
		s = markdown.ToSpeech(s, markdown.KeepOverrides())
	}
	if o.Normalize {
		// Misaki reads punctuation for prosody and pronunciation, so it stays,
		// and reads capitals, not lower-case letters, as letter names.
		s = normalize.Text(s, normalize.KeepPunctuation(), normalize.UpperLetters(), normalize.ProtectOverrides())
	}
	return s
}

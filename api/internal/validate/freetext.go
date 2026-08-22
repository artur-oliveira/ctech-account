package validate

import (
	"fmt"
	"strings"
	"unicode"
)

// FreetextRule bounds one free-text field. Min/Max are inclusive, measured
// in runes after trimming.
type FreetextRule struct {
	Min int
	Max int
}

// minLetterRatio is the floor for (letters / non-whitespace runes) after
// collapsing repeated-character runs. Below this, the input reads as
// keyboard-mashing or punctuation spam rather than a sentence — this is a
// bot/low-effort filter, not a content-quality judge.
const minLetterRatio = 0.3

// maxRepeat is how many identical consecutive runes are tolerated before the
// input is rejected as junk (e.g. "aaaaaaaaaaaaaaaa", "..........").
const maxRepeat = 4

// Freetext trims s, checks its length against rule, and rejects low-signal
// junk (long repeated-character runs, or too few letters relative to
// length). Returns the trimmed string on success.
func Freetext(s string, rule FreetextRule) (string, error) {
	trimmed := strings.TrimSpace(s)
	runeLen := len([]rune(trimmed))
	if runeLen < rule.Min {
		return "", fmt.Errorf("must be at least %d characters long", rule.Min)
	}
	if runeLen > rule.Max {
		return "", fmt.Errorf("must be at most %d characters long", rule.Max)
	}
	if hasLongRepeat(trimmed) {
		return "", fmt.Errorf("looks like repeated characters, not a real message")
	}
	if !hasEnoughLetters(trimmed) {
		return "", fmt.Errorf("must contain readable text")
	}
	return trimmed, nil
}

func hasLongRepeat(s string) bool {
	runs := 1
	var prev rune
	for i, r := range s {
		if i == 0 {
			prev = r
			continue
		}
		if r == prev {
			runs++
			if runs > maxRepeat {
				return true
			}
		} else {
			runs = 1
			prev = r
		}
	}
	return false
}

func hasEnoughLetters(s string) bool {
	var letters, nonSpace int
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		nonSpace++
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if nonSpace == 0 {
		return false
	}
	return float64(letters)/float64(nonSpace) >= minLetterRatio
}

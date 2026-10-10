package accounts

import "strings"

// Handles are public on the leaderboard, so ValidateHandle rejects slurs and obscenities.
// The check undoes the tricks a 3–24 character [a-z0-9_-] handle allows: digits for
// letters (sh1t), separators (f-u-c-k) and repeated letters (fuuuck).
//
// Two lists avoid the Scunthorpe problem. Terms in blockedAnywhere don't occur inside
// ordinary words, so they're blocked anywhere in a handle. Terms in blockedWords do occur
// inside ordinary words (ass in "classic", rape in "grape", spic in "spice"), so they're
// only blocked as a whole word, or as the whole handle once separators are removed.
//
// It's a filter, not a guarantee: anything it misses can be renamed with paperperps-admin.

var blockedAnywhere = []string{
	"asshole", "bitch", "bastard", "bollock", "bukkake", "cocksuck", "cunt", "dickhead",
	"dildo", "faggot", "fuck", "goatse", "hitler", "jizz", "kkk", "masturbat", "molest",
	"motherfuck", "nazi", "nigga", "nigger", "paedo", "pedophil", "porn", "pussies",
	"retard", "shit", "slut", "twat", "whore", "heilhitler", "siegheil",
	"whitepower", "gaschamber", "killjews", "killallj",
}

var blockedWords = []string{
	"anal", "anus", "ass", "boob", "boobs", "chink", "cock", "coon", "cum", "dick", "dyke",
	"fag", "gook", "kike", "nig", "pedo", "penis", "piss", "poof", "pussy", "rape", "raped",
	"rapist", "sex", "spic", "tit", "tits", "tranny", "vagina", "wank", "wanker", "wetback",
}

// leet maps digits people use for letters. 1 is tried as both i and l.
var leet = strings.NewReplacer("0", "o", "3", "e", "4", "a", "5", "s", "7", "t", "8", "b", "9", "g")

// spellings returns the ways a piece of a handle can be read, with digits as letters.
func spellings(s string) []string {
	base := leet.Replace(s)
	return []string{strings.ReplaceAll(base, "1", "i"), strings.ReplaceAll(base, "1", "l")}
}

// A term is matched as runs of letters, each run matching that many or more of its
// letter, so "fuck" also catches "fuuuck" but "boobs" doesn't catch "bobs".
type run struct {
	c byte
	n int
}

func runsOf(term string) []run {
	var rs []run
	for i := 0; i < len(term); i++ {
		if len(rs) > 0 && rs[len(rs)-1].c == term[i] {
			rs[len(rs)-1].n++
		} else {
			rs = append(rs, run{term[i], 1})
		}
	}
	return rs
}

// matchAt returns where a match of rs starting at text[i] ends. Adjacent runs have
// different letters, so taking every repeat greedily is always right.
func matchAt(text string, i int, rs []run) (int, bool) {
	for _, r := range rs {
		k := i
		for k < len(text) && text[k] == r.c {
			k++
		}
		if k-i < r.n {
			return 0, false
		}
		i = k
	}
	return i, true
}

func compile(terms []string) [][]run {
	out := make([][]run, len(terms))
	for i, t := range terms {
		out[i] = runsOf(t)
	}
	return out
}

var (
	anywhereRuns = compile(blockedAnywhere)
	wordRuns     = compile(blockedWords)
)

// containsTerm reports whether any term occurs anywhere in text.
func containsTerm(text string, terms [][]run) bool {
	for _, rs := range terms {
		for i := 0; i < len(text); i++ {
			if text[i] != rs[0].c {
				continue
			}
			if _, ok := matchAt(text, i, rs); ok {
				return true
			}
		}
	}
	return false
}

// isTerm reports whether text is exactly a term, optionally plural ("tits", "dicks").
func isTerm(text string, terms [][]run) bool {
	for _, rs := range terms {
		if end, ok := matchAt(text, 0, rs); ok && strings.Trim(text[end:], "s") == "" {
			return true
		}
	}
	return false
}

// Offensive reports whether a lowercase handle contains a blocked term.
func Offensive(handle string) bool {
	joined := strings.NewReplacer("-", "", "_", "").Replace(handle)
	for _, s := range spellings(joined) {
		if containsTerm(s, anywhereRuns) || isTerm(s, wordRuns) {
			return true
		}
	}
	for _, word := range strings.FieldsFunc(handle, func(r rune) bool { return r == '-' || r == '_' }) {
		// A trailing number is decoration, not leetspeak: "ass-123" is "ass".
		word = strings.TrimRight(word, "0123456789")
		for _, s := range spellings(word) {
			if isTerm(s, wordRuns) {
				return true
			}
		}
	}
	return false
}

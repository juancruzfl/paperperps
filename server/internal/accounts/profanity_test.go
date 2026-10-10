package accounts

import (
	"errors"
	"fmt"
	"testing"
)

func TestOffensiveBlocks(t *testing.T) {
	for _, h := range []string{
		"fuck", "fuckface", "shit-trader", "sh1t", "5hit", "f-u-c-k", "f_u_c_k", "fuuuuck", "phat-fuuck-1",
		"n1gger", "nigga99", "faggot", "fagg0t", "c0ck", "cock-123", "big-dick", "dicks", "tits",
		"a-s-s", "ass", "asshole", "kkk", "kkkk", "hitler-fan", "heil-hitler", "retard", "rapist",
		"spic", "wanker", "whore", "porn-king", "cunt", "b1tch", "biiitch",
	} {
		if !Offensive(h) {
			t.Errorf("Offensive(%q) = false, want true", h)
		}
		if err := ValidateHandle(h); len(h) >= 3 && !errors.Is(err, ErrHandleOffensive) {
			t.Errorf("ValidateHandle(%q) = %v, want ErrHandleOffensive", h, err)
		}
	}
}

// Terms that occur inside ordinary words must not block them (the Scunthorpe problem).
func TestOffensiveAllows(t *testing.T) {
	for _, h := range []string{
		"classic", "grassland", "bass-fisher", "assassin", "passport", "massive-gains", "embassy",
		"scrape", "grape-ape", "drape", "therapist", "analyst", "canal", "cumulative", "document",
		"cucumber", "sussex", "essex", "spice-trader", "dickens", "hancock", "peacock", "cockatoo",
		"title", "titan", "petition", "pedometer", "torpedo", "swanky", "bobs-burgers", "homo-sapiens",
		"jewel", "night-owl", "kit-kat", "as-if", "mississippi", "bookkeeper", "cockpit-ace",
		// Known misses, accepted: "snigger" and "shiitake" are blocked, since letting the
		// slur and the stretched obscenity through would be worse.
	} {
		if Offensive(h) {
			t.Errorf("Offensive(%q) = true, want false", h)
		}
	}
}

// Handles the server generates for new accounts must pass its own filter. Every
// adjective-animal pair, and every animal with every number (digits read as letters can
// only join the word next to them), covers what randomHandle can produce.
func TestRandomHandlesAreClean(t *testing.T) {
	check := func(h string) {
		if err := ValidateHandle(h); err != nil {
			t.Fatalf("generated handle %q fails validation: %v", h, err)
		}
	}
	for _, a := range adjectives {
		for _, b := range animals {
			check(fmt.Sprintf("%s-%s-000", a, b))
		}
	}
	for _, b := range animals {
		for n := range 1000 {
			check(fmt.Sprintf("%s-%s-%03d", adjectives[0], b, n))
		}
	}
}

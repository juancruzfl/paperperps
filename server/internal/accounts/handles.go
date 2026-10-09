package accounts

import (
	"fmt"
	"math/rand/v2"
)

var (
	adjectives = []string{
		"brave", "calm", "clever", "cosmic", "crisp", "daring", "eager", "fancy", "fearless",
		"gentle", "golden", "happy", "humble", "jolly", "keen", "lively", "lucky", "mellow",
		"mighty", "nimble", "noble", "patient", "plucky", "quick", "quiet", "rapid", "sly",
		"steady", "sunny", "swift", "tidy", "witty", "zesty", "bold", "breezy", "frosty",
	}
	animals = []string{
		"otter", "falcon", "badger", "panda", "lynx", "heron", "koala", "marten", "ibex",
		"gecko", "bison", "puffin", "walrus", "jaguar", "kestrel", "lemur", "moose", "narwhal",
		"ocelot", "orca", "pelican", "quokka", "raven", "seal", "tapir", "toucan", "viper",
		"wombat", "yak", "zebra", "beaver", "coyote", "dingo", "ferret", "hare", "mantis",
	}
)

// randomHandle returns a public display name like "brave-otter-412". It is not secret.
func randomHandle() string {
	return fmt.Sprintf("%s-%s-%03d",
		adjectives[rand.N(len(adjectives))], animals[rand.N(len(animals))], rand.N(1000))
}

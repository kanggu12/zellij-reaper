package rename

import (
	"regexp"
	"slices"
	"strings"
)

// Generated reports whether a name is one zellij made up for a session nobody
// named: an adjective and a noun from its word lists, as in "brave-peach". Such a
// name says nothing about the session, so auto_rename may replace it. A name in any
// other shape was chosen by someone. Looking at the shape alone would not do:
// "web-app" and "api-server" have it too.
func Generated(name string) bool {
	adjective, noun, ok := strings.Cut(name, "-")
	return ok && slices.Contains(zellijAdjectives, adjective) && slices.Contains(zellijNouns, noun)
}

// stampedRe matches the names the shell version and releases before 0.2 gave: a
// base, the time of last use as a real month, day, hour and minute, and perhaps a
// collision tail. "report_2025-2026" and "ticket_1234-5678" have the shape of one
// without being a date.
var stampedRe = regexp.MustCompile(`_(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])-([01]\d|2[0-3])[0-5]\d(-\d{1,2})?$`)

// Stamped reports whether a name carries that stamp. Nothing recorded those names,
// so the stamp is how they are told from a name someone chose.
func Stamped(name string) bool { return stampedRe.MatchString(name) }

// zellijAdjectives and zellijNouns are the words zellij makes names from, in
// zellij-utils/src/sessions.rs (MIT, zellij-LICENSE.md next to this file). The
// adjectives are the same in every release from 0.43.1 to 0.45.1; the nouns are all
// those releases' together. `make integration` compares them with the source of each
// release it tests, through TestKnowsTheWordsOfARelease.
var zellijAdjectives = []string{
	"adamant", "adept", "adventurous", "arcadian", "auspicious", "awesome", "blossoming", "brave",
	"charming", "chatty", "circular", "considerate", "cubic", "curious", "delighted", "didactic",
	"diligent", "effulgent", "erudite", "excellent", "exquisite", "fabulous", "fascinating", "friendly",
	"glowing", "gracious", "gregarious", "hopeful", "implacable", "inventive", "joyous", "judicious",
	"jumping", "kind", "likable", "loyal", "lucky", "marvellous", "mellifluous", "nautical", "oblong",
	"outstanding", "polished", "polite", "profound", "quadratic", "quiet", "rectangular", "remarkable",
	"rusty", "sensible", "sincere", "sparkling", "splendid", "stellar", "tenacious", "tremendous",
	"triangular", "undulating", "unflappable", "unique", "verdant", "vitreous", "wise", "zippy",
}

var zellijNouns = []string{
	"aardvark", "accordion", "apple", "apricot", "bee", "brachiosaur", "cactus", "capsicum", "clarinet",
	"cowbell", "crab", "cuckoo", "cymbal", "diplodocus", "donkey", "drum", "duck", "echidna",
	"elephant", "foxglove", "galaxy", "glockenspiel", "goose", "hill", "horse", "iguanadon",
	"iguanodon", "jellyfish", "kangaroo", "lake", "lemon", "lemur", "magpie", "megalodon", "mountain",
	"mouse", "muskrat", "newt", "oboe", "ocelot", "orange", "panda", "peach", "pepper", "petunia",
	"pheasant", "piano", "pigeon", "platypus", "quasar", "rhinoceros", "river", "rustacean",
	"salamander", "sitar", "stegosaurus", "tambourine", "tiger", "tomato", "triceratops", "ukulele",
	"viola", "weasel", "xylophone", "yak", "zebra",
}

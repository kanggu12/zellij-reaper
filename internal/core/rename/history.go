package rename

// A Change is one rename the decision log holds.
type Change struct {
	// From and To are the names before and after.
	From, To string
	// ByHand is a rename asked for in the dashboard; the others are a pass's.
	ByHand bool
}

// GivenByPass reports whether renames, oldest first, say a pass gave a session its
// name. The last rename to that name has to be a pass's. Following each name back
// through the rename that gave it then has to end at the name the session started
// under: a rename to the same name by a session now gone ends somewhere else, and so
// does a history whose start the log no longer holds.
func GivenByPass(changes []Change, name, startedAs string) bool {
	if startedAs == "" || name == startedAs {
		return false
	}
	end := len(changes)
	for cur, last := name, true; ; last = false {
		i := lastTo(changes[:end], cur)
		// The first name of this history: it has to be the one the session
		// started under, and some rename has to have led away from it.
		if i < 0 {
			return !last && cur == startedAs
		}
		// The name it has now came from a person.
		if last && changes[i].ByHand {
			return false
		}
		cur, end = changes[i].From, i
	}
}

// lastTo is the index of the last change to name, or -1.
func lastTo(changes []Change, name string) int {
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i].To == name {
			return i
		}
	}
	return -1
}

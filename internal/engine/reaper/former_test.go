package reaper_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// formerWorld is the usual world with a book of former names, renaming on, and
// a record of every name zellij is asked for.
func formerWorld(t *testing.T, w *world, refuses bool) (*world, *[]string) {
	t.Helper()
	w.env.FormerPath = filepath.Join(t.TempDir(), "state", "former")
	w.env.RefusesFormerNames = refuses
	w.env.Config.AutoRename = true
	asked := &[]string{}
	w.zj.onRename = func(to string) { *asked = append(*asked, to) }
	return w, asked
}

// formerKey is the busy session's key in the books, as the survey reads it:
// its server, when that started, and the name it started under.
func formerKey(t *testing.T, w *world) string {
	t.Helper()
	r, err := reaper.Plan(context.Background(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Outcomes {
		if s := o.Session; s.Name() == "busy" || strings.HasPrefix(s.Name(), "htop") {
			return strconv.Itoa(s.Server.PID) + "\t" + strconv.FormatInt(s.ServerStart, 10) + "\t" + s.StartedAs()
		}
	}
	t.Fatal("no busy session")
	return ""
}

// writeFormer writes a book in which the busy session had these names.
func writeFormer(t *testing.T, w *world, names ...string) {
	t.Helper()
	book := map[string]any{"version": 1, "sessions": map[string]any{
		formerKey(t, w): map[string]any{"names": names, "seen": 1},
	}}
	raw, err := json.Marshal(book)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(w.env.FormerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.env.FormerPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// readFormer is the names the book holds for the busy session, and whether
// the book is there at all.
func readFormer(t *testing.T, w *world) ([]string, bool) {
	t.Helper()
	raw, err := os.ReadFile(w.env.FormerPath)
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var book struct {
		Version  int
		Sessions map[string]struct{ Names []string }
	}
	if err := json.Unmarshal(raw, &book); err != nil || book.Version != 1 {
		t.Fatalf("book %s: %v", raw, err)
	}
	return book.Sessions[formerKey(t, w)].Names, true
}

// names runs a names pass.
func names(t *testing.T, w *world) {
	t.Helper()
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
}

// look runs a full pass that renames nothing: the names timer's pass does
// nothing without auto_rename.
func look(t *testing.T, w *world) {
	t.Helper()
	w.env.Config.AutoRename = false
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestANameTheSessionHadGetsATailWithoutAsking(t *testing.T) {
	// The busy session was called htop before: zellij 0.44 and 0.45 refuse it
	// that name, and with a client attached the pass would only see the old
	// name stay, after a wait. Nobody asks.
	w, asked := formerWorld(t, newWorld(t), true)
	writeFormer(t, w, "htop")
	names(t, w)
	if strings.Join(*asked, ",") != "htop-2" || strings.Join(w.zj.renamed, ",") != "busy->htop-2" {
		t.Errorf("asked %v, renamed %v", *asked, w.zj.renamed)
	}
	// The dashboard offers the same.
	r, err := reaper.Plan(context.Background(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Outcomes {
		if o.Session.Name() == "busy" && o.Suggested != "htop-2" {
			t.Errorf("suggested %q", o.Suggested)
		}
	}
	// A zellij that gives such a name back, 0.43 or the main branch, is
	// asked for it, and its book is not filled with every name it sees.
	w, asked = formerWorld(t, newWorld(t), false)
	names(t, w)
	if strings.Join(*asked, ",") != "htop" {
		t.Errorf("a zellij that gives names back: asked %v", *asked)
	}
	if _, ok := readFormer(t, w); ok {
		t.Error("a book written for a zellij that gives names back")
	}
}

func TestAPassWritesDownTheNamesItSees(t *testing.T) {
	w, _ := formerWorld(t, newWorld(t), true)
	look(t, w)
	got, ok := readFormer(t, w)
	if !ok || strings.Join(got, ",") != "busy" {
		t.Errorf("book %v (%v)", got, ok)
	}
	// Another pass that sees the same adds nothing.
	look(t, w)
	if again, _ := readFormer(t, w); strings.Join(again, ",") != "busy" {
		t.Errorf("again %v", again)
	}
}

func TestARefusedNameIsLearned(t *testing.T) {
	// The session had htop before the book knew of it, by hand between two
	// passes say: zellij keeps the old name, the pass asks for the next, and
	// the book learns htop, so the next pass does not ask for it.
	w, asked := formerWorld(t, newWorld(t), true)
	w.zj.keep = map[string]bool{"htop": true}
	names(t, w)
	if strings.Join(*asked, ",") != "htop,htop-2" || strings.Join(w.zj.renamed, ",") != "busy->htop-2" {
		t.Errorf("asked %v, renamed %v", *asked, w.zj.renamed)
	}
	if got, _ := readFormer(t, w); strings.Join(got, ",") != "busy,htop" {
		t.Errorf("book %v", got)
	}
	// The fake lists the session as busy still, a name a pass gave: the
	// next pass renames it again, straight to the tail.
	again := func(w *world) {
		*asked, w.zj.renamed = nil, nil
		if err := w.env.Given.Set("busy", true); err != nil {
			t.Fatal(err)
		}
		names(t, w)
	}
	again(w)
	if strings.Join(*asked, ",") != "htop-2" {
		t.Errorf("next pass asked %v", *asked)
	}
	// A server that gives names back (0.43, the main branch) did not act on
	// the rename: a failure, and no name learned.
	w, asked = formerWorld(t, newWorld(t), false)
	w.zj.keep = map[string]bool{"htop": true}
	r, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Outcomes {
		if o.Session.Name() == "busy" && (o.RenameErr == nil || errors.Is(o.RenameErr, zellij.ErrKept)) {
			t.Errorf("a server that gives names back: %v", o.RenameErr)
		}
	}
	if got, ok := readFormer(t, w); ok && len(got) > 0 {
		t.Errorf("a server that gives names back taught the book %v", got)
	}
	again(w)
	if strings.Join(*asked, ",") != "htop" {
		t.Errorf("a server that gives names back, next pass asked %v", *asked)
	}
	// A session zellij names by id runs 0.46 or later, whatever the zellij on
	// the PATH: nothing is written down for it.
	w, _ = formerWorld(t, newWorldOf(t, "busy", "quiet-mouse", true, true), true)
	look(t, w)
	if got, ok := readFormer(t, w); ok && len(got) > 0 {
		t.Errorf("a session named by id: book %v", got)
	}
}

func TestANameRefusedWithNoClientIsLearnedToo(t *testing.T) {
	// With no client attached, zellij says a name the session had exists:
	// the pass asks for the tail at once, and the book learns the name.
	w, asked := formerWorld(t, newWorld(t), true)
	w.zj.had = map[string]bool{"htop": true}
	names(t, w)
	if strings.Join(*asked, ",") != "htop,htop-2" || strings.Join(w.zj.renamed, ",") != "busy->htop-2" {
		t.Errorf("asked %v, renamed %v", *asked, w.zj.renamed)
	}
	if got, _ := readFormer(t, w); strings.Join(got, ",") != "busy,htop" {
		t.Errorf("book %v", got)
	}
	// Any number of them: no wait, so no bound but the tails.
	w, asked = formerWorld(t, newWorld(t), true)
	w.zj.had = map[string]bool{"htop": true, "htop-2": true, "htop-3": true, "htop-4": true}
	names(t, w)
	if strings.Join(w.zj.renamed, ",") != "busy->htop-5" {
		t.Errorf("asked %v, renamed %v", *asked, w.zj.renamed)
	}
}

func TestAServerThatKeepsEveryNameStopsAfterThree(t *testing.T) {
	// A session that came back to htop four times has four names zellij keeps
	// the old one instead of, the book knowing none: the first three are
	// refusals, the fourth a failure, and the next pass goes on from there.
	w, asked := formerWorld(t, newWorld(t), true)
	w.zj.keep = map[string]bool{"htop": true, "htop-2": true, "htop-3": true, "htop-4": true}
	r, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*asked, ",") != "htop,htop-2,htop-3,htop-4" || len(w.zj.renamed) != 0 {
		t.Errorf("asked %v, renamed %v", *asked, w.zj.renamed)
	}
	for _, o := range r.Outcomes {
		if o.Session.Name() == "busy" && !errors.Is(o.RenameErr, zellij.ErrKept) {
			t.Errorf("busy: %v", o.RenameErr)
		}
	}
	*asked = nil
	names(t, w)
	if strings.Join(*asked, ",") != "htop-5" || strings.Join(w.zj.renamed, ",") != "busy->htop-5" {
		t.Errorf("next pass asked %v, renamed %v", *asked, w.zj.renamed)
	}
}

func TestATailIsNotDroppedForANameTheSessionHad(t *testing.T) {
	// htop-2 may drop its tail once no other session is htop, unless it was
	// htop itself before: zellij would refuse, so nobody asks.
	w, asked := formerWorld(t, newWorldWith(t, "htop-2", "quiet-mouse", true), true)
	w.env.Config.AutoRenameDropTail = true
	writeFormer(t, w, "htop", "htop-2")
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 {
		t.Errorf("asked %v", *asked)
	}
	// Without that, the tail goes.
	w, asked = formerWorld(t, newWorldWith(t, "htop-2", "quiet-mouse", true), true)
	w.env.Config.AutoRenameDropTail = true
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*asked, ",") != "htop" {
		t.Errorf("without the book: asked %v", *asked)
	}
}

func TestTheBookOfFormerNamesForgetsWhatNoPassSees(t *testing.T) {
	w, _ := formerWorld(t, newWorld(t), true)
	// A session of a day ago, whose server is gone.
	book := `{"version":1,"sessions":{"9\t1\told":{"names":["old"],"seen":1}}}`
	if err := os.MkdirAll(filepath.Dir(w.env.FormerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.env.FormerPath, []byte(book), 0o600); err != nil {
		t.Fatal(err)
	}
	look(t, w)
	raw, err := os.ReadFile(w.env.FormerPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"old"`) || !strings.Contains(string(raw), `"busy"`) {
		t.Errorf("book %s", raw)
	}
	if info, err := os.Stat(w.env.FormerPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("book mode: %v %v", info, err)
	}
	// A book that cannot be read is an empty one.
	if err := os.WriteFile(w.env.FormerPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	look(t, w)
	if got, ok := readFormer(t, w); !ok || strings.Join(got, ",") != "busy" {
		t.Errorf("after a broken book: %v (%v)", got, ok)
	}
}

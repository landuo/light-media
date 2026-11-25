package removelink

// These are synthetic failure-injection tests of the Go core, using real files
// only inside t.TempDir and an in-process Ops recorder. They are not a Python
// oracle, external notification delivery, or real concurrent-channel evidence.
// Expected catch boundaries were independently read in frozen source.py:
// empty-directory line 1230 catches inside each ancestor iteration; delayed
// line 1388 catches before finally processed=True; immediate line 1585 catches
// in the hardlink block; STRM line 2069 catches in the outer STRM block.
// ValueError is also a narrow typed source outcome: notice/template_assignment.go
// rejects assignment to model methods, and notice/schedule.go parses clock values.
// Explicit typed RuntimeError is covered by notice/original_test.go router tests;
// plain errors with the same class text are never evidence of a source exception.

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type barrierNotice struct{ Site, Title, Text string }
type barrierOps struct {
	*fixtureOps
	failSite string
	failure  error
	notices  []barrierNotice
	writes   []string
}

func (o *barrierOps) NoticeAt(site, title, text string) error {
	o.notices = append(o.notices, barrierNotice{site, title, text})
	o.writes = append(o.writes, "notice:"+site)
	if site == o.failSite {
		return o.failure
	}
	return nil
}
func (o *barrierOps) Notice(title, text string) error {
	return errors.New("site-aware core unexpectedly used legacy Notice")
}
func (o *barrierOps) Unlink(p string) error {
	o.writes = append(o.writes, "unlink:"+p)
	return o.fixtureOps.Unlink(p)
}
func (o *barrierOps) RemoveDir(p string) error {
	o.writes = append(o.writes, "rmdir:"+p)
	return o.fixtureOps.RemoveDir(p)
}
func (o *barrierOps) RemoveTree(p string) error {
	o.writes = append(o.writes, "rmtree:"+p)
	return o.fixtureOps.RemoveTree(p)
}
func (o *barrierOps) Event(p string) error {
	o.writes = append(o.writes, "event:"+p)
	return o.fixtureOps.Event(p)
}
func (o *barrierOps) History(kind, p string) (bool, error) {
	o.writes = append(o.writes, "history:"+kind+":"+p)
	return o.fixtureOps.History(kind, p)
}
func (o *barrierOps) StorageDelete(item Item) (bool, error) {
	o.writes = append(o.writes, "storage_delete:"+item.Path)
	return o.fixtureOps.StorageDelete(item)
}

func barrierMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func barrierSetup(t *testing.T, site string, failure error) (*Machine, *barrierOps, string, string) {
	t.Helper()
	root := t.TempDir()
	// A non-scrap owner marker also prevents traversal beyond this owned root.
	barrierMust(t, os.WriteFile(filepath.Join(root, "owner.marker"), []byte("owned"), 0600))
	src := filepath.Join(root, "source", "season", "movie.mkv")
	link := filepath.Join(root, "library", "movie.mkv")
	barrierMust(t, os.MkdirAll(filepath.Dir(src), 0700))
	barrierMust(t, os.MkdirAll(filepath.Dir(link), 0700))
	barrierMust(t, os.WriteFile(src, []byte("synthetic media"), 0600))
	barrierMust(t, os.Link(src, link))
	ops := &barrierOps{fixtureOps: &fixtureOps{root: root, media: []string{".mkv"}}, failSite: site, failure: failure}
	m := New(ops)
	m.Now = func() time.Time { return time.Unix(1000, 0) }
	m.Config.Enabled = true
	m.Config.Notify = true
	m.Config.History = true
	m.Config.Torrents = true
	m.Config.Delayed = false
	id, err := ops.Stat(src)
	barrierMust(t, err)
	m.State = []Tracked{{src, id}, {link, id}}
	barrierMust(t, os.Remove(src))
	return m, ops, src, link
}
func barrierHasWrite(ops *barrierOps, want string) bool {
	for _, got := range ops.writes {
		if got == want {
			return true
		}
	}
	return false
}

func TestSourceNoticeKnownCatchBoundariesSynthetic(t *testing.T) {
	for _, site := range []string{"empty_directory", "hardlink_immediate", "hardlink_delayed", "strm"} {
		for _, class := range []string{"", "TypeError", "AttributeError", "ValueError", "RuntimeError"} {
			t.Run(site+"/"+map[string]string{"": "accepted", "TypeError": "TypeError", "AttributeError": "AttributeError", "ValueError": "ValueError", "RuntimeError": "RuntimeError"}[class], func(t *testing.T) {
				var failure error
				if class != "" {
					failure = &SourceNoticeError{Class: class}
				}
				m, ops, src, link := barrierSetup(t, site, failure)
				var task *Task
				switch site {
				case "empty_directory":
					m.Config.Scrap = true
				case "hardlink_delayed":
					m.Config.Delayed = true
				case "strm":
					m.Config.Mappings = filepath.Dir(src) + ":local:/remote"
					ops.storage = []Item{{Storage: "local", Path: "/remote/", Type: "dir"}, {Storage: "local", Path: "/remote/movie.mkv", Type: "file", Name: "movie.mkv", Extension: "mkv"}}
					src = strings.TrimSuffix(src, ".mkv") + ".strm"
				}
				mode := "hardlink"
				if site == "strm" {
					mode = "strm"
				}
				barrierMust(t, m.Deleted(src, mode, false))
				if site == "hardlink_delayed" {
					if len(m.Queue) != 1 {
						t.Fatalf("queue=%v", m.Queue)
					}
					task = m.Queue[0]
					m.Now = func() time.Time { return time.Unix(2000, 0) }
					m.Tick()
					if !task.Processed || len(m.Queue) != 0 {
						t.Fatal("source finally did not mark failed-notice task processed")
					}
				}
				if site == "strm" {
					if !barrierHasWrite(ops, "storage_delete:/remote/movie.mkv") || !barrierHasWrite(ops, "history:dest:/remote/movie.mkv") {
						t.Fatalf("prior STRM effects missing: %v", ops.writes)
					}
					for _, item := range ops.storage {
						if item.Path == "/remote/movie.mkv" {
							t.Fatal("notice failure rolled back storage deletion")
						}
					}
				} else {
					if ops.Exists(link) || !barrierHasWrite(ops, "history:src:"+link) || !barrierHasWrite(ops, "event:"+link) {
						t.Fatalf("prior or following authorized effects missing: %v", ops.writes)
					}
				}
				if site == "empty_directory" {
					if ops.Exists(filepath.Dir(filepath.Dir(src))) {
						t.Fatal("caught source notice error aborted next ancestor")
					}
					if len(ops.notices) < 3 || ops.notices[0].Site != "empty_directory" || ops.notices[1].Site != "empty_directory" || ops.notices[len(ops.notices)-1].Site != "hardlink_immediate" {
						t.Fatalf("per-ancestor catch failed: %+v", ops.notices)
					}
				} else if len(ops.notices) != 1 || ops.notices[0].Site != site {
					t.Fatalf("wrong source barrier: %+v", ops.notices)
				}
				wantWrites := []string{"event:" + src, "history:src:" + src, "unlink:" + link, "event:" + link, "history:src:" + link, "notice:" + site}
				if site == "empty_directory" {
					wantWrites = []string{
						"rmdir:" + filepath.Dir(src), "notice:empty_directory",
						"rmdir:" + filepath.Dir(filepath.Dir(src)), "notice:empty_directory",
						"event:" + src, "history:src:" + src, "unlink:" + link,
						"rmdir:" + filepath.Dir(link), "notice:empty_directory",
						"event:" + link, "history:src:" + link, "notice:hardlink_immediate",
					}
				} else if site == "strm" {
					wantWrites = []string{"storage_delete:/remote/movie.mkv", "history:dest:/remote/movie.mkv", "notice:strm"}
				}
				if !reflect.DeepEqual(ops.writes, wantWrites) {
					t.Fatalf("source-ordered synthetic effects differ:\nwant %v\ngot %v", wantWrites, ops.writes)
				}
				if len(m.Errors) != 0 {
					t.Fatalf("known or accepted notice became fatal: %v", m.Errors)
				}
				wantSourceErrors := 0
				if class != "" {
					wantSourceErrors = 1
					if site == "empty_directory" {
						wantSourceErrors = 3
					}
				}
				if len(m.SourceErrors) != wantSourceErrors {
					t.Fatalf("caught source diagnostics: want %d, got %v", wantSourceErrors, m.SourceErrors)
				}
			})
		}
	}
}

func TestSourceNoticeUnknownEmptyStopsBeforeParentAndLinksSynthetic(t *testing.T) {
	m, ops, src, link := barrierSetup(t, "empty_directory", errors.New("synthetic journal unavailable"))
	m.Config.Scrap = true
	// Unknown infrastructure failure is deliberately safer than Python Exception:
	// preserve already removed child, but do not authorize another parent or link.
	_ = m.Deleted(src, "hardlink", false)
	if ops.Exists(filepath.Dir(src)) {
		t.Fatal("prior child-directory effect was rolled back")
	}
	if !ops.Exists(filepath.Dir(filepath.Dir(src))) || !ops.Exists(link) {
		t.Fatalf("unknown notice crossed source barrier: %v", ops.writes)
	}
	if len(ops.notices) != 1 {
		t.Fatalf("continued notice effects: %+v", ops.notices)
	}
	before := append([]string(nil), ops.writes...)
	m.Queue = append(m.Queue, &Task{Path: src, Identity: m.State[len(m.State)-1].Identity, At: time.Unix(0, 0)})
	m.Tick()
	m.Stop()
	_ = m.Deleted(link, "hardlink", false)
	if !reflect.DeepEqual(before, ops.writes) {
		t.Fatalf("unknown failure did not latch across future entrypoints: before=%v after=%v", before, ops.writes)
	}
}

func TestSourceNoticeUnknownStopsLaterTimerTasksSynthetic(t *testing.T) {
	for _, failure := range []error{errors.New("synthetic notice commit uncertainty"), errors.New("TypeError: synthetic unclassified transport failure"), errors.New("RuntimeError: synthetic unclassified callback failure"), &SourceNoticeError{Class: "UnprovenClass"}} {
		t.Run(failure.Error(), func(t *testing.T) {
			m, ops, src, link := barrierSetup(t, "hardlink_delayed", failure)
			m.Config.Delayed = true
			barrierMust(t, m.Deleted(src, "hardlink", false))
			first := m.Queue[0]
			later := filepath.Join(ops.root, "library", "later.mkv")
			barrierMust(t, os.WriteFile(later, []byte("other owned media"), 0600))
			laterID, err := ops.Stat(later)
			barrierMust(t, err)
			m.State = append(m.State, Tracked{later, laterID})
			second := &Task{Path: filepath.Join(ops.root, "missing-later.mkv"), Identity: laterID, At: time.Unix(0, 0)}
			m.Queue = append(m.Queue, second)
			m.Now = func() time.Time { return time.Unix(2000, 0) }
			m.Tick()
			if !first.Processed || ops.Exists(link) {
				t.Fatal("prior task effects or delayed finally lost")
			}
			if !ops.Exists(later) || second.Processed {
				t.Fatalf("unknown notice authorized next task: %v", ops.writes)
			}
			before := append([]string(nil), ops.writes...)
			m.Tick()
			m.Stop()
			if !ops.Exists(later) || !reflect.DeepEqual(before, ops.writes) {
				t.Fatalf("later timer/stop escaped fatal latch: %v", ops.writes)
			}
		})
	}
}

func TestSourceNoticeSiteSequenceStableAcrossSyntheticReplay(t *testing.T) {
	// Ordinals here are positions in the source invocation sequence, not proof of
	// the host's durable per-action journal ordinals or real delivery retries.
	var previous []string
	for replay := 0; replay < 2; replay++ {
		m, ops, src, _ := barrierSetup(t, "empty_directory", &SourceNoticeError{Class: "TypeError"})
		m.Config.Scrap = true
		barrierMust(t, m.Deleted(src, "hardlink", false))
		sequence := []string{}
		for _, n := range ops.notices {
			sequence = append(sequence, n.Site+":"+strings.ReplaceAll(n.Text, ops.root, "@ROOT@"))
		}
		if replay > 0 && !reflect.DeepEqual(previous, sequence) {
			t.Fatalf("source site/order changed across synthetic replay: %v / %v", previous, sequence)
		}
		previous = sequence
	}
}

func TestSourceNoticeUnknownFinalSitesKeepPriorEffectsSynthetic(t *testing.T) {
	for _, site := range []string{"hardlink_immediate", "strm"} {
		t.Run(site, func(t *testing.T) {
			m, ops, src, link := barrierSetup(t, site, errors.New("synthetic notice journal failure"))
			if site == "strm" {
				m.Config.Mappings = filepath.Dir(src) + ":local:/remote"
				ops.storage = []Item{{Storage: "local", Path: "/remote/", Type: "dir"}, {Storage: "local", Path: "/remote/movie.mkv", Type: "file", Name: "movie.mkv", Extension: "mkv"}}
				src = strings.TrimSuffix(src, ".mkv") + ".strm"
			}
			mode := "hardlink"
			if site == "strm" {
				mode = "strm"
			}
			_ = m.Deleted(src, mode, false)
			if len(ops.notices) != 1 || ops.notices[0].Site != site {
				t.Fatalf("missing failure site: %+v", ops.notices)
			}
			if site == "strm" {
				if !barrierHasWrite(ops, "storage_delete:/remote/movie.mkv") || !barrierHasWrite(ops, "history:dest:/remote/movie.mkv") {
					t.Fatalf("lost prior STRM effects: %v", ops.writes)
				}
			} else if ops.Exists(link) || !barrierHasWrite(ops, "history:src:"+link) {
				t.Fatalf("lost prior hardlink effects: %v", ops.writes)
			}
			later := filepath.Join(ops.root, "library", "later.mkv")
			barrierMust(t, os.WriteFile(later, []byte("other owned media"), 0600))
			id, err := ops.Stat(later)
			barrierMust(t, err)
			m.State = append(m.State, Tracked{later, id})
			task := &Task{Path: filepath.Join(ops.root, "later-source.mkv"), Identity: id, At: time.Unix(0, 0)}
			m.Queue = append(m.Queue, task)
			before := append([]string(nil), ops.writes...)
			m.Tick()
			m.Stop()
			if task.Processed || !ops.Exists(later) || !reflect.DeepEqual(before, ops.writes) {
				t.Fatalf("unknown final notice escaped barrier: %v", ops.writes)
			}
		})
	}
}

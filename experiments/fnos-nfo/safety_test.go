package main

import "testing"

const syntheticID = "00000000-0000-4000-8000-000000000001"

func TestOwnershipAndIsolation(t *testing.T) {
	baseline := []library{{GUID: "existing", Name: "Real library", Category: "Movie", Dirs: []string{"/vol2/1000/media/movies"}}}
	r, e := newReceipt(syntheticID, "synthetic-host", baseline)
	if e != nil {
		t.Fatal(e)
	}
	own := library{GUID: "new-owned", Name: r.Name, Category: "Mix", Dirs: []string{r.Root}}
	libs := append(baseline, own)
	if _, e = ownLibrary(r, libs); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []library{{GUID: "new-owned", Name: r.Name, Category: "Mix", Dirs: []string{r.Root, "/vol2/1000/media/movies"}}, {GUID: "existing", Name: r.Name, Category: "Mix", Dirs: []string{r.Root}}, {GUID: "new-owned", Name: r.Name, Category: "Movie", Dirs: []string{r.Root}}} {
		if _, e = ownLibrary(r, []library{bad}); e == nil {
			t.Fatal("unsafe library accepted")
		}
	}
	for _, root := range []string{"/", "/vol2/1000/media", r.Root + "/child"} {
		if _, e = newReceipt(syntheticID, "synthetic-host", []library{{GUID: "old", Name: "other", Dirs: []string{root}}}); e == nil {
			t.Fatal("overlap allowed")
		}
	}
}

func TestCanonicalTrailingSlash(t *testing.T) {
	root := parentRoot + "/" + prefix + syntheticID
	for _, candidate := range []string{root, root + "/", root + "//"} {
		if !samePath(candidate, root) {
			t.Fatal("valid trailing slash rejected")
		}
	}
	for _, candidate := range []string{root + "/../other", root + "/./", root + "-sibling", root + "/child", "relative"} {
		if samePath(candidate, root) {
			t.Fatal("unsafe root accepted")
		}
	}
	baseline := []library{{GUID: "old", Name: "old", Category: "Movie", Dirs: []string{"/safe/"}}}
	r, e := newReceipt(syntheticID, "host", baseline)
	if e != nil {
		t.Fatal(e)
	}
	own := library{GUID: "new", Name: r.Name, Category: "Mix", Dirs: []string{r.Root + "/"}}
	if _, e = ownLibrary(r, append(baseline, own)); e != nil {
		t.Fatal(e)
	}
	own.GUID = "old"
	if _, e = ownLibrary(r, []library{own}); e == nil {
		t.Fatal("baseline GUID accepted")
	}
}

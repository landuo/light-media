package app

import "testing"

func TestDirectoryStorageMoveRequiresCompleteMemberList(t *testing.T) {
	entries := []originalDirectoryEntry{{Path: "BDMV", Directory: true}, {Path: "BDMV/one.m2ts"}, {Path: "small.txt"}}
	files := []string{"BDMV/one.m2ts", "small.txt"}
	members := map[string]originalDirectoryMember{"BDMV/one.m2ts": {Target: "BluRay/feature.m2ts"}, "small.txt": {Target: "small.txt"}}
	if err := validateDirectoryStorageMoveList(entries, files, members); err != nil {
		t.Fatal("complete renamed list", err)
	}
	if err := validateDirectoryStorageMoveList(entries, files[:1], members); err == nil {
		t.Fatal("partial list accepted")
	}
	members["small.txt"] = originalDirectoryMember{Target: "BluRay/feature.m2ts"}
	if err := validateDirectoryStorageMoveList(entries, files, members); err == nil {
		t.Fatal("colliding targets accepted")
	}
	members["BDMV/one.m2ts"] = originalDirectoryMember{Target: "Videos"}
	members["small.txt"] = originalDirectoryMember{Target: "Videos/index.bdmv"}
	if err := validateDirectoryStorageMoveList(entries, files, members); err == nil {
		t.Fatal("file/directory prefix conflict accepted")
	}
}

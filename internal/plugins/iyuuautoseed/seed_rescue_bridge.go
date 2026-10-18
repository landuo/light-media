package iyuuautoseed

import "strings"

// SourceNoReseedData exposes only SeedRescue's original no-match predicate.
// It does not reveal server text and is never a dispatch/authority proof.
func SourceNoReseedData(f *Failure) bool {
	return f != nil && strings.Contains(f.sourceMessage, "状态码：404") && strings.Contains(f.sourceMessage, "未查询到可辅种数据")
}

package brushflow

import pyre "light-media/internal/plugins/iyuuautoseed/compat/pythonre"

var sourcePatterns = map[string]*pyre.Pattern{
	"PROMOTION_DATE_PATTERN":                    mustPattern("(?i)(\\d{4})[-/.](\\d{1,2})[-/.](\\d{1,2})(?:[ T](\\d{1,2}):(\\d{2})(?::(\\d{2}))?)?\\s*(Z|UTC|GMT|[+-]\\d{2}:?\\d{2})?"),
	"PROMOTION_DURATION_PATTERN":                mustPattern("(?i)(\\d+)\\s*(天|日|小时|时|分钟|分|hours?|hrs?|minutes?|mins?|days?)"),
	"PROMOTION_LABELFREE_PATTERN":               mustPattern("(\\d+):(\\d{2})(?::(\\d{2}))?"),
	"PROMOTION_PAGE_REMAINING_PATTERN":          mustPattern("(?s)剩余时间[^<]{0,40}(?:<b\\b[^>]*>\\s*)?<span[^>]*title=[\\\"']([^\\\"']+)[\\\"']"),
	"PROMOTION_PAGE_REMAINING_FALLBACK_PATTERN": mustPattern("(?i)(?:剩余时间|剩余|免费截止|优惠截止|促销截止|Free\\s+until|until)\\s*[:：]?\\s*([0-9]{4}[-/.][0-9]{1,2}[-/.][0-9]{1,2}(?:[ T][0-9]{1,2}:[0-9]{2}(?::[0-9]{2})?)?)"),
	"PROMOTION_PAGE_MARKER_PATTERN":             mustPattern("(?i)class=[\\\"'][^\\\"']*?(?<![\\w-])(?:pro_free(?:2up)?|free|promotion-tag-(?:2x)?free)(?![\\w-])"),
	"PROMOTION_PAGE_OTHER_VERSIONS_PATTERN":     mustPattern("(?i)<[^>]+\\bid=[\\\"']kothercopy[\\\"'][^>]*>"),
	"PROMOTION_PAGE_NONE_PATTERN":               mustPattern("无优惠|不享受种子促销|不享受促销|无促销"),
	"PROMOTION_PAGE_REMAINING_HINT_PATTERN":     mustPattern("剩余时间|剩余\\s*[:：]"),
	"PROMOTION_PAGE_TORRENT_PATTERN":            mustPattern("(?i)href=[\\\"'][^\\\"']*download\\.php\\?id="),
}

func mustPattern(s string) *pyre.Pattern {
	p, e := pyre.Compile(s)
	if e != nil {
		panic(e)
	}
	return p
}

// Generated from pinned TransHandler.__rename_subtitles by generate-transfer-subtitle-oracle.py.
package app

var originalTransferSubtitlePatterns = []struct{ Language, Tag, Pattern string }{
	{"zh-tw", ".zh-tw", "([.\\[(\\s](((zh[-_])?(hk|tw|cht|tc))|cht[-_&]?(cht|eng|jap|ja|jpn)|eng[-_&]?cht|(jap|ja|jpn)[-_&]?cht|繁[体中]?)[.\\])\\s])|繁体中[文字]|中[文字]繁体|繁体|JPTC|tc_jp|(?<![a-z0-9])big5(?![a-z0-9])"},
	{"zh-cn", ".chi.zh-cn", "([.\\[(\\s](((zh[-_])?(cn|ch[si]|sg|sc))|zho?|chinese|(cn|ch[si]|sg|zho?)[-_&]?(cn|ch[si]|sg|zho?|eng|jap|ja|jpn)|eng[-_&]?(cn|ch[si]|sg|zho?)|(jap|ja|jpn)[-_&]?(cn|ch[si]|sg|zho?)|简[体中]?)[.\\])\\s])|([\\u4e00-\\u9fa5]{0,3}[中双][\\u4e00-\\u9fa5]{0,2}[字文语][\\u4e00-\\u9fa5]{0,3})|简体|简中|JPSC|sc_jp|(?<![a-z0-9])gb(?![a-z0-9])"},
	{"ja", ".ja", "([.\\[(\\s](ja-jp|jap|ja|jpn|(jap|ja|jpn)[-_&]?eng|eng[-_&]?(jap|ja|jpn))[.\\])\\s])|日本語|日語"},
	{"eng", ".eng", "[.\\[(\\s]eng[.\\])\\s]"},
}

package releasemeta

// Patterns are copied from the installed MoviePilot MetaVideo, SHA ecb5a1ba6830f9e5eae2b6e26e42738845b6b80f73f1f4ece431dbbf0e229077.
var videoPatterns = map[string]string{
	"_season_re":          "S(\\d{3})|^S(\\d{1,3})$|S(\\d{1,3})E",
	"_episode_re":         "EP?(\\d{2,4})$|^EP?(\\d{1,4})$|^S\\d{1,2}EP?(\\d{1,4})$|S\\d{2}EP?(\\d{2,4})",
	"_part_re":            "(^PART[0-9ABI]{0,2}$|^CD[0-9]{0,2}$|^DVD[0-9]{0,2}$|^DISK[0-9]{0,2}$|^DISC[0-9]{0,2}$)",
	"_roman_numerals":     "^(?=[MDCLXVI])M*(C[MD]|D?C{0,3})(X[CL]|L?X{0,3})(I[XV]|V?I{0,3})$",
	"_source_re":          "^BLURAY$|^HDTV$|^UHDTV$|^HDDVD$|^WEBRIP$|^DVDRIP$|^BDRIP$|^BLU$|^WEB$|^BD$|^HDRip$|^REMUX$|^UHD$",
	"_effect_re":          "^SDR$|^HDR\\d*$|^HDRVIVID$|^DOLBY$|^DOVI$|^DV$|^3D$|^REPACK$|^HLG$|^HDR10(\\+|Plus)$|^HDR10P$|^VIVID$|^EDR$|^HQ$",
	"_resources_type_re":  "^BLURAY$|^HDTV$|^UHDTV$|^HDDVD$|^WEBRIP$|^DVDRIP$|^BDRIP$|^BLU$|^WEB$|^BD$|^HDRip$|^REMUX$|^UHD$|^SDR$|^HDR\\d*$|^HDRVIVID$|^DOLBY$|^DOVI$|^DV$|^3D$|^REPACK$|^HLG$|^HDR10(\\+|Plus)$|^HDR10P$|^VIVID$|^EDR$|^HQ$",
	"_name_no_chinese_re": ".*版|.*字幕",
	"_name_nostring_re":   "^PTS|^JADE|^AOD|^CHC|^[A-Z]{1,4}TV[\\-0-9UVHDK]*|\\d{1,2}th|\\d{1,2}bit|IMAX|^3D|\\s+3D|\\s+DC$|[第\\s共]+[0-9一二三四五六七八九十\\-\\s]+季|[第\\s共]+[0-9一二三四五六七八九十百零\\-\\s]+[集话話]|连载|日剧|美剧|电视剧|动画片|动漫|欧美|西德|日韩|超高清|高清|无水印|下载|蓝光|翡翠台|梦幻天堂·龙网|★?\\d*月?新番|最终季|合集|[多中国英葡法俄日韩德意西印泰台港粤双文语简繁体特效内封官译外挂]+字幕|版本|出品|台版|港版|\\w+字幕组|\\w+字幕社|未删减版|UNCUT$|UNRATE$|WITH EXTRAS$|RERIP$|SUBBED$|PROPER$|REPACK$|SEASON$|EPISODE$|Complete$|Extended$|Extended Version$|S\\d{2}\\s*-\\s*S\\d{2}|S\\d{2}|\\s+S\\d{1,2}|EP?\\d{2,4}\\s*-\\s*EP?\\d{2,4}|EP?\\d{2,4}|\\s+EP?\\d{1,4}|CD[\\s.]*[1-9]|DVD[\\s.]*[1-9]|DISK[\\s.]*[1-9]|DISC[\\s.]*[1-9]|[248]K|\\d{3,4}[PIX]+|CD[\\s.]*[1-9]|DVD[\\s.]*[1-9]|DISK[\\s.]*[1-9]|DISC[\\s.]*[1-9]|\\s+GB",
	"_resources_pix_re":   "^[SBUHD]*(\\d{3,4}[PI]+)|\\d{3,4}X(\\d{3,4})",
	"_resources_pix_re2":  "(^[248]+K)",
	"_video_encode_re":    "^(H26[45])$|^(x26[45])$|^AVC$|^HEVC$|^VC\\d?$|^MPEG\\d?$|^Xvid$|^DivX$|^AV1$|^HDR\\d*$|^AVS(\\+|[23])$",
	"_audio_encode_re":    "^DTS\\d?$|^DTSHD$|^DTSHDMA$|^Atmos$|^TrueHD\\d?$|^AC3$|^\\dAudios?$|^DDP\\d?$|^DD\\+\\d?$|^DD\\d?$|^LPCM\\d?$|^AAC\\d?$|^FLAC\\d?$|^HD\\d?$|^MA\\d?$|^HR\\d?$|^Opus\\d?$|^Vorbis\\d?$|^AV[3S]A$",
}

// Built-in release-group alternatives retain source order; user expressions follow.
const builtinGroups = "FF(?:(?:A|WE)B|CD|E(?:DU|B)|TV)|Audies|AD(?:Audio|E(?:book|)|Music|Web)|BeiTai|Bts(?:CHOOL|HD|PAD|TV)|Zone|CarPT|CHD(?:Bits|PAD|(?:|HK)TV|WEB|)|StBOX|OneHD|Lee|xiaopie|(?:(?:iNT|(?:HALFC|Mini(?:S|H|FH)D))-|)TLF|(?:DG|GBWE)B|Hares(?:(?:M|T)V|Web|)|HDA(?:pad|rea|TV)|EPiC|HDC(?:hina|TV|)|k9611|tudou|iHD|D(?:ream|BTV)|(?:HD|QHstudI)o|beAst(?:TV|)|HDH(?:ome|Pad|TV|WEB|)|HDPT(?:Web|)|HDS(?:ky|TV|Pad|WEB|)|AQLJ|HDZ(?:one|)|HHWEB|HTPT|FRDS|Yumi|cXcY|L(?:eague(?:(?:C|H)D|(?:M|T)V|NF|WEB)|HD)|i18n|CiNT|MTeam(?:TV|)|MPAD|MWeb|Our(?:Bits|TV)|FLTTH|Ao|PbK|MGs|iLove(?:HD|TV)|Panda|AilMWeb|PiGo(?:NF|(?:H|WE)B)|PTer(?:DIY|Game|(?:M|T)V|WEB|)|PTH(?:Audio|eBook|music|ome|tv|WEB|)|PTsbao|OPS|F(?:Fans(?:AIeNcE|BD|D(?:VD|IY)|TV|WEB)|HDMv)|SGXT|PuTao|CMCT(?:V|)|Shark(?:WEB|DIY|TV|MV|)|TJUPT|TTG|WiKi|NGB|DoA|(?:ARi|ExRE)N|B(?:MDru|eyondHD|TN)|C(?:fandora|trlhd|MRG)|DON|EVO|FLUX|HONE(?:yG|)|N(?:oGroup|T(?:b|G))|PandaMoon|SMURF|T(?:EPES|aengoo|rollHD )|ANi|HYSUB|KTXP|LoliHouse|MCE|Nekomoe kissaten|SweetSub|MingY|(?:Lilith|NC)-Raws|织梦字幕组|枫叶字幕组|猎户手抄部|喵萌奶茶屋|漫猫字幕社|霜庭云花Sub|北宇治字幕组|氢气烤肉架|云歌字幕组|萌樱字幕组|极影字幕社|悠哈璃羽字幕社|❀拨雪寻春❀|沸羊羊(?:制作|字幕组)|(?:桜|樱)都字幕组|FROG(?:E|Web|)|UB(?:its|WEB|TV)"

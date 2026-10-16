package cleaninvalidseed

import (
	"context"
	"fmt"
	"strings"

	py "light-media/internal/releasemeta/legacyanime"
)

type torrentTuple struct{ name, category, tags, size, domain, msg any }
type notWorking struct {
	t      *Object
	errors []string
}

func tuple(t *Object, kind, domain string, msg any) (torrentTuple, error) {
	name, e := required(t, "name")
	if e != nil {
		return torrentTuple{}, e
	}
	cat, e := Category(t, kind)
	if e != nil {
		return torrentTuple{}, e
	}
	tags, e := Tags(t, kind)
	if e != nil {
		return torrentTuple{}, e
	}
	fallback, e := attr(t, "total_size", 0)
	if e != nil {
		return torrentTuple{}, e
	}
	size, e := attr(t, "size", fallback)
	return torrentTuple{name, cat, tags, size, domain, msg}, e
}
func formatTuple(t torrentTuple, i int) string {
	return fmt.Sprintf("  %d. 📁 %s\n     📂 分类：%s | 🏷️ 标签：%s | 📏 大小：%s\n     🌐 Tracker：%s - %s\n", i+1, str(t.name), str(t.category), str(t.tags), FileSize(t.size), str(t.domain), str(t.msg))
}
func trackerFields(t any) (url, status, msg, tier any, e error) {
	url, e = dictGet(t, "url", nil)
	if e != nil {
		return
	}
	status, e = dictGet(t, "status", nil)
	if e != nil {
		return
	}
	msg, e = dictGet(t, "msg", "")
	if e != nil {
		return
	}
	tier, e = dictGet(t, "tier", nil)
	return
}
func (m *Machine) inspectTorrent(t *Object, kind string, working map[string]bool, errs []string) (invalid, trackerWorking bool, details []string, e error) {
	trackers := m.TrackerInfo(t, kind)
	if Truth(m.Config["more_logs"]) {
		if _, e = required(t, "name"); e != nil {
			return
		}
	}
	valid := false
	details = []string{}
	for _, tr := range trackers {
		u, status, msg, tier, err := trackerFields(tr)
		if err != nil {
			e = err
			return
		}
		if eq(tier, -1) {
			continue
		}
		d, err := URLNetloc(u)
		if err != nil {
			e = err
			return
		}
		if eq(status, 2) || eq(status, 3) {
			trackerWorking = true
			valid = true
			working[d[1]] = true
		}
		isErr := eq(status, 4) && contains(errs, msg)
		if !isErr {
			valid = true
			working[d[1]] = true
		}
	}
	invalid = !valid
	if Truth(m.Config["more_logs"]) {
		if _, e = Category(t, kind); e != nil {
			return
		}
		if _, e = Tags(t, kind); e != nil {
			return
		}
	}
	if !invalid && !trackerWorking {
		paused, err := Paused(t, kind)
		if err != nil {
			e = err
			return
		}
		if !paused {
			for _, tr := range trackers {
				u, status, msg, tier, err := trackerFields(tr)
				if err != nil {
					e = err
					return
				}
				if eq(tier, -1) {
					continue
				}
				d, err := URLNetloc(u)
				if err != nil {
					e = err
					return
				}
				if !eq(status, 2) && !eq(status, 3) {
					desc := "状态" + str(status)
					if eq(status, 0) {
						desc = "未知"
					} else if eq(status, 1) {
						desc = "等待中"
					} else if eq(status, 4) {
						desc = "错误"
					}
					if Truth(msg) {
						details = append(details, d[1]+"("+desc+": "+str(msg)+")")
					} else {
						details = append(details, d[1]+"("+desc+")")
					}
				}
			}
		} else {
			details = nil
		}
	}
	return
}
func (m *Machine) Clean(ctx context.Context) error {
	services, e := m.services(ctx)
	if e != nil {
		return e
	}
	for _, s := range services {
		if s.FalseInstance {
			continue
		}
		m.log(ctx, "info", "开始清理 "+s.Name+" ("+s.Kind+") 无效做种...")
		m.log(ctx, "info", "正在获取 "+s.Name+" 的种子列表...")
		all, e := m.getAll(ctx, s)
		if e != nil {
			return e
		}
		m.log(ctx, "info", fmt.Sprintf("获取到 %d 个种子，开始分析...", len(all)))
		temp := []*Object{}
		nw := []notWorking{}
		working := map[string]bool{}
		cats, e := lines(m.Config["exclude_categories"])
		if e != nil {
			return e
		}
		labels, e := lines(m.Config["exclude_labels"])
		if e != nil {
			return e
		}
		errs, e := m.customErrors()
		if e != nil {
			return e
		}
		for index, t := range all {
			if (index+1)%50 == 0 {
				m.log(ctx, "info", fmt.Sprintf("正在处理第 %d/%d 个种子...", index+1, len(all)))
			}
			invalid, active, details, err := m.inspectTorrent(t, s.Kind, working, errs)
			if err != nil {
				m.log(ctx, "error", "处理种子tracker时出错: "+err.Error())
				continue
			}
			if invalid {
				temp = append(temp, t)
			} else if !active && details != nil {
				nw = append(nw, notWorking{t, details})
			}
		}
		m.log(ctx, "info", fmt.Sprintf("初筛共有%d个无效做种", len(temp)))
		catExcluded := []*Object{}
		labelExcluded := []*Object{}
		invalidTuples := []torrentTuple{}
		deleted := []torrentTuple{}
		for _, t := range temp {
			trackers := m.TrackerInfo(t, s.Kind)
			for _, tr := range trackers {
				u, _, msg, tier, e := trackerFields(tr)
				if e != nil {
					return e
				}
				if eq(tier, -1) {
					continue
				}
				domain, e := URLNetloc(u)
				if e != nil {
					return e
				}
				if !working[domain[1]] {
					continue
				}
				item, e := tuple(t, s.Kind, domain[1], msg)
				if e != nil {
					return e
				}
				invalidTuples = append(invalidTuples, item)
				if Truth(m.Config["delete_invalid_torrents"]) || Truth(m.Config["label_only"]) {
					excluded := false
					if contains(cats, item.category) {
						excluded = true
						catExcluded = append(catExcluded, t)
					}
					tags, e := stringValue(item.tags, "split")
					if e != nil {
						return e
					}
					for _, tag := range strings.Split(tags, ",") {
						tag = py.Strip(tag)
						if tag != "" && contains(labels, tag) {
							excluded = true
							labelExcluded = append(labelExcluded, t)
						}
					}
					if !excluded {
						hash, e := Hash(t, s.Kind)
						if e != nil {
							return e
						}
						if Truth(hash) {
							if Truth(m.Config["label_only"]) {
								label := m.Config["label"]
								if eq(label, "") {
									label = "无效做种"
								}
								var tags []any
								if s.Kind == "qbittorrent" {
									tags = []any{label}
								} else {
									tags = []any{}
									existing, e := attr(t, "labels", []any{})
									if e != nil {
										return e
									}
									if Truth(existing) {
										a, e := listValue(existing)
										if e != nil {
											return e
										}
										for _, x := range a {
											tags = append(tags, str(x))
										}
									}
									found := false
									for _, x := range tags {
										if eq(x, label) {
											found = true
										}
									}
									if !found {
										tags = append(tags, label)
									}
								}
								if _, e = m.Host.SetTags(ctx, s, hash, tags); e != nil {
									return e
								}
							} else {
								if _, e = m.Host.DeleteTorrent(ctx, s, hash, false); e != nil {
									return e
								}
							}
							deleted = append(deleted, item)
						}
					}
				}
				break
			}
		}
		invalidMsg := "✅ 未发现失效做种，所有种子状态正常\n"
		if len(invalidTuples) > 0 {
			invalidMsg = fmt.Sprintf("🔍 检测到 %d 个失效做种\n", len(invalidTuples))
		}
		for i, v := range invalidTuples {
			invalidMsg += formatTuple(v, i)
		}
		notMsg := "✅ 所有 tracker 工作正常\n"
		if len(nw) > 0 {
			notMsg = fmt.Sprintf("⚠️ 检测到 %d 个 tracker 未工作的种子，请检查种子状态\n", len(nw))
		}
		for i, v := range nw {
			item, e := tuple(v.t, s.Kind, "", "")
			if e != nil {
				return e
			}
			notMsg += fmt.Sprintf("  %d. 📁 %s\n     📂 分类：%s | 🏷️ 标签：%s | 📏 大小：%s\n", i+1, str(item.name), str(item.category), str(item.tags), FileSize(item.size))
			if len(v.errors) > 0 {
				notMsg += "     🌐 Tracker错误：" + strings.Join(v.errors, ", ") + "\n"
			} else {
				notMsg += "     🌐 Tracker：未工作\n"
			}
		}
		deleteMsg := ""
		action := Truth(m.Config["label_only"]) || Truth(m.Config["delete_invalid_torrents"])
		if action {
			if Truth(m.Config["label_only"]) {
				deleteMsg = "✅ 无需标记任何种子\n"
				if len(deleted) > 0 {
					deleteMsg = fmt.Sprintf("🏷️ 已标记 %d 个失效种子\n", len(deleted))
				}
			} else {
				deleteMsg = "✅ 无需删除任何种子\n"
				if len(deleted) > 0 {
					deleteMsg = fmt.Sprintf("🗑️ 已删除 %d 个失效种子\n", len(deleted))
				}
			}
		}
		for i, v := range deleted {
			deleteMsg += formatTuple(v, i)
		}
		catMsg, labelMsg := "", ""
		if action && len(cats) > 0 {
			catMsg = fmt.Sprintf("🏷️ 分类过滤：%d 个失效种子因分类设置未处理，请手动检查\n", len(catExcluded))
		}
		if action && len(labels) > 0 {
			labelMsg = fmt.Sprintf("🏷️ 标签过滤：%d 个失效种子因标签设置未处理，请手动检查\n", len(labelExcluded))
		}
		for i, t := range catExcluded {
			item, e := tuple(t, s.Kind, "", "")
			if e != nil {
				return e
			}
			catMsg += fmt.Sprintf("  %d. 📁 %s (📂 %s, 📏 %s)\n", i+1, str(item.name), str(item.category), FileSize(item.size))
		}
		for i, t := range labelExcluded {
			item, e := tuple(t, s.Kind, "", "")
			if e != nil {
				return e
			}
			labelMsg += fmt.Sprintf("  %d. 📁 %s (🏷️ %s, 📏 %s)\n", i+1, str(item.name), str(item.tags), FileSize(item.size))
		}
		m.log(ctx, "info", invalidMsg)
		m.log(ctx, "info", notMsg)
		if Truth(m.Config["delete_invalid_torrents"]) {
			m.log(ctx, "info", deleteMsg)
			if len(cats) > 0 {
				m.log(ctx, "info", catMsg)
			}
			if len(labels) > 0 {
				m.log(ctx, "info", labelMsg)
			}
		}
		if Truth(m.Config["notify"]) {
			send := func(title, text string) error {
				text = strings.ReplaceAll(text, "_", "\\_")
				return m.notice(ctx, title, &text)
			}
			if e = send("🧹 【清理无效做种】", invalidMsg); e != nil {
				return e
			}
			if Truth(m.Config["notify_all"]) {
				if e = send("📊 【清理无效做种 - 详细信息】", notMsg); e != nil {
					return e
				}
			}
			if action {
				if e = send("🗑️ 【清理无效做种 - 删除结果】", deleteMsg); e != nil {
					return e
				}
				if Truth(m.Config["notify_all"]) {
					if len(cats) == 0 {
						return source("UnboundLocalError", "exclude_categories_msg")
					}
					if e = send("🏷️ 【清理无效做种 - 分类过滤】", catMsg); e != nil {
						return e
					}
					if len(labels) == 0 {
						return source("UnboundLocalError", "exclude_labels_msg")
					}
					if e = send("🏷️ 【清理无效做种 - 标签过滤】", labelMsg); e != nil {
						return e
					}
				}
			}
		}
		m.log(ctx, "info", "检测无效做种任务结束")
		if Truth(m.Config["detect_invalid_files"]) {
			if e = m.DetectFiles(ctx); e != nil {
				return e
			}
		}
	}
	return nil
}

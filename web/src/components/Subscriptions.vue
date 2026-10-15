<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue';
import {
  api,
  action,
  ui,
  emptyPolicy,
  emptySchedule,
  episodeNumbers,
  image,
  date,
  label,
  go,
  clone,
} from '../api';
import {
  subscriptionTarget,
  subscriptionTotal,
  validateSubscriptionCounts,
} from '../features/subscriptions';
import {
  calendarDay,
  calendarDates,
  moveCalendar,
  calendarGroups,
  calendarEpisodes,
  type CalendarView,
} from '../features/subscription-calendar';
import Policy from './Policy.vue';
import Schedule from './Schedule.vue';
import MappingRules from './MappingRules.vue';
import MultiSeasonSubscribe from './MultiSeasonSubscribe.vue';
import DownloadTarget from './DownloadTarget.vue';
import { downloadDestination } from '../features/download-directories';
const props = defineProps<{ view: string }>(),
  subs = ref<any[]>([]),
  entries = ref<any[]>([]),
  editing = ref<any>(null),
  multiSeason = ref(false),
  multiTemplate = ref<any>(null),
  convertLegacy = ref(false),
  filterKind = ref(new URLSearchParams(props.view.split('?')[1]).get('type') || 'all'),
  filterState = ref('all'),
  filterText = ref(''),
  sortBy = ref('created'),
  excluded = ref(''),
  allowEarly = ref(''),
  dates = ref(''),
  lookup = ref(''),
  lookupResults = ref<any[]>([]),
  editingTab = ref('basic'),
  sitesOpen = ref(false),
  selectedEvent = ref<any>(null),
  loading = ref(false);
const calendarView = ref<CalendarView>('month'),
  calendarAnchor = ref(calendarDay(new Date()));
const siteOptions = computed(() => (ui.settings?.sites || []).filter((site: any) => site.enabled));
const selectedSites = computed(() =>
  (editing.value?.site_ids || []).map(
    (id: string) => ui.settings?.sites?.find((site: any) => site.id === id) || { id, name: id },
  ),
);
const filteredSubs = computed(() =>
  subs.value
    .filter(
      (s) =>
        (filterKind.value === 'all' || s.media.media_type === filterKind.value) &&
        (filterState.value === 'all' || s.state === filterState.value) &&
        (!filterText.value || s.media.title.includes(filterText.value)),
    )
    .sort((a, b) =>
      sortBy.value === 'name'
        ? a.media.title.localeCompare(b.media.title, 'zh-CN')
        : sortBy.value === 'missing'
          ? (b.missing_episodes?.length || 0) - (a.missing_episodes?.length || 0)
          : String(b.created_at || '').localeCompare(String(a.created_at || '')),
    ),
);
function totalEpisodes(s: any) {
  return subscriptionTotal(s);
}
function percent(s: any) {
  return totalEpisodes(s)
    ? Math.min(100, Math.round(((s.in_library_episodes?.length || 0) / totalEpisodes(s)) * 100))
    : 0;
}
const isCalendar = computed(() => props.view.split('?')[0] === 'calendar');
const days = computed(() => {
  try {
    return calendarDates(calendarAnchor.value, calendarView.value);
  } catch {
    return [];
  }
});
const groupedEntries = computed(() => calendarGroups(entries.value));
const agendaDays = computed(() =>
  days.value.filter((day) => groupedEntries.value.some((entry) => entry.date === day)),
);
const calendarTitle = computed(() => {
  if (calendarView.value === 'week')
    return days.value.length ? days.value[0] + ' — ' + days.value[6] : '';
  const [year, month] = calendarAnchor.value.split('-');
  return year + '年' + Number(month) + '月';
});
const eventSubscription = computed(() =>
  subs.value.find((sub) => sub.id === selectedEvent.value?.subscription_id),
);
function eventState(entry: any) {
  return (
    (
      {
        upcoming: '未播出',
        available: '已播出',
        in_library: '已入库',
        reserved: '下载中',
        excluded: '已排除',
        skipped: '已跳过',
        pending_confirmation: '预测更新',
      } as Record<string, string>
    )[entry.state] || label(entry.state || entry.status)
  );
}
function eventSource(entry: any) {
  return (
    (
      { metadata: '元数据日期', manual_episode: '人工日期', prediction: '周期预测' } as Record<
        string,
        string
      >
    )[entry.source] || entry.source
  );
}
function eventComplete(entry: any) {
  return entry.entries.every((row: any) => row.state === 'in_library');
}
function toggleSite(id: string) {
  const values = editing.value.site_ids || [];
  editing.value.site_ids = values.includes(id)
    ? values.filter((value: string) => value !== id)
    : [...values, id];
}
function setSeason(value: number) {
  editing.value.season = value;
  editing.value.total_episodes =
    editing.value.media?.seasons?.find((season: any) => season.season_number === value)
      ?.episode_count || 0;
}
function editEvent() {
  if (eventSubscription.value) {
    begin(eventSubscription.value);
    selectedEvent.value = null;
  }
}
let loadGeneration = 0;
async function load() {
  const generation = ++loadGeneration;
  loading.value = true;
  try {
    const response = await api('/subscriptions');
    if (generation !== loadGeneration) return;
    subs.value = Array.isArray(response) ? response : response?.items || [];
    if (isCalendar.value) {
      const visible = days.value.filter(Boolean);
      if (!visible.length) throw new Error('请选择有效的日历日期');
      const r = await api('/calendar?from=' + visible[0] + '&to=' + visible[visible.length - 1]);
      if (generation === loadGeneration) entries.value = r.entries || [];
    }
  } catch (e) {
    await action(() => Promise.reject(e));
  } finally {
    if (generation === loadGeneration) loading.value = false;
  }
}
function begin(s?: any) {
  editing.value = s
    ? clone(s)
    : {
        id: '',
        media: null,
        season: 1,
        episodes: [],
        target_scope: 'season',
        start_episode: 1,
        total_episodes: 0,
        site_ids: [],
        keyword: '',
        excluded: [],
        episode_dates: {},
        allow_early: [],
        policy: emptyPolicy(),
        schedule: emptySchedule(),
        mappings: [],
        downloader_id: '',
        download_path: '',
        state: 'active',
      };
  multiSeason.value = false;
  editingTab.value = 'basic';
  sitesOpen.value = false;
  convertLegacy.value = false;
  editing.value.start_episode ||= 1;
  editing.value.download_path ||= '';
  editing.value.site_ids ||= [];
  editing.value.total_episodes ??=
    editing.value.media?.seasons?.find(
      (season: any) => season.season_number === editing.value.season,
    )?.episode_count || 0;
  excluded.value = (editing.value.excluded || []).join(',');
  allowEarly.value = (editing.value.allow_early || []).join(',');
  dates.value = Object.entries(editing.value.episode_dates || {})
    .map(([ep, d]) => `${ep}=${d}`)
    .join('\n');
  editing.value.policy = { ...emptyPolicy(), ...editing.value.policy };
  editing.value.schedule = { ...emptySchedule(), ...editing.value.schedule };
  editing.value.mappings ||= [];
  for (const m of editing.value.mappings) m.source ||= { site: '', group: '', form: '' };
}
async function find() {
  lookupResults.value =
    (await action(() => api('/catalog/search?q=' + encodeURIComponent(lookup.value) + '&page=1')))
      ?.results || [];
}
async function pick(m: any) {
  const form = editing.value;
  const media = await action(() => api('/catalog/detail?kind=' + m.media_type + '&id=' + m.id));
  if (editing.value !== form) return;
  editing.value.media = media;
  editing.value.season =
    editing.value.media?.media_type === 'movie'
      ? 0
      : editing.value.media?.seasons?.find((s: any) => s.season_number > 0)?.season_number || 1;
  editing.value.target_scope = editing.value.media?.media_type === 'movie' ? 'movie' : 'season';
  setSeason(editing.value.season);
  lookupResults.value = [];
}
function formInput() {
  const s = clone(editing.value);
  if (!s.media) throw new Error('请先选择影视作品');
  Object.assign(s, downloadDestination(s));
  delete s.root_id;
  delete s.directory_id;
  s.total_episodes = Number(s.total_episodes || 0);
  validateSubscriptionCounts(s);
  s.excluded = episodeNumbers(excluded.value);
  s.allow_early = episodeNumbers(allowEarly.value);
  s.episode_dates = {};
  for (const row of dates.value
    .split('\n')
    .map((x) => x.trim())
    .filter(Boolean)) {
    const match = row.match(/^(\d+)=(\d{4}-\d{2}-\d{2})$/);
    if (!match) throw new Error('单集日期格式：17=2026-10-16');
    s.episode_dates[match[1]!] = match[2];
  }
  s.media_type = s.media.media_type;
  s.tmdb_id = s.media.id;
  return s;
}
async function openMulti() {
  await action(async () => {
    multiTemplate.value = formInput();
    multiSeason.value = true;
  });
}
function closeMulti(created: boolean) {
  multiSeason.value = false;
  if (created) {
    editing.value = null;
    if (props.view.includes('/new/')) go('subscriptions');
  }
}
async function save() {
  const r = await action(async () => {
    const s = formInput();
    Object.assign(s, subscriptionTarget(s, convertLegacy.value));
    return api('/subscriptions' + (s.id ? '/' + s.id : ''), s, s.id ? 'PUT' : 'POST');
  }, '订阅已保存');
  if (r) {
    editing.value = null;
    await load();
    if (props.view.includes('/new/')) go('subscriptions');
  }
}
async function remove(s: any) {
  if (!confirm('删除《' + s.media.title + '》的订阅？已下载文件会保留。')) return;
  const r = await action(() => api('/subscriptions/' + s.id, undefined, 'DELETE'), '订阅已删除');
  if (r !== undefined) load();
}
async function check(s: any) {
  await action(() => api('/subscriptions/' + s.id + '/check', {}), '检查任务已提交');
  load();
}
async function state(s: any) {
  await action(
    () =>
      api(
        '/subscriptions/' + s.id,
        { ...s, state: s.state === 'paused' ? 'active' : 'paused' },
        'PUT',
      ),
    '订阅状态已更新',
  );
  load();
}
function month(offset: number) {
  calendarAnchor.value = moveCalendar(calendarAnchor.value, calendarView.value, offset);
  load();
}
function today() {
  calendarAnchor.value = calendarDay(new Date());
  load();
}
function showCalendar(view: CalendarView) {
  calendarView.value = view;
  load();
}
async function applyView() {
  const view = props.view;
  selectedEvent.value = null;
  await load();
  if (view !== props.view) return;
  if (props.view.includes('/new/')) {
    begin();
    const [path, qs] = props.view.split('?'),
      p = path!.split('/'),
      q = new URLSearchParams(qs);
    await pick({ media_type: p[2], id: +p[3]! });
    if (!editing.value || view !== props.view) return;
    setSeason(
      editing.value.media?.media_type === 'movie' ? 0 : +(q.get('season') || editing.value.season),
    );
    editing.value.start_episode = Math.max(1, +(q.get('start_episode') || 1));
  }
}
onMounted(applyView);
watch(() => props.view, applyView);
</script>
<template>
  <div v-if="!isCalendar" class="page-actions">
    <button class="primary" @click="begin()">＋ 新建订阅</button>
  </div>
  <template v-if="isCalendar">
    <div class="calendar-toolbar">
      <div class="button-row">
        <button aria-label="上一页" @click="month(-1)">‹</button><button @click="today">今天</button
        ><button aria-label="下一页" @click="month(1)">›</button>
      </div>
      <h2>{{ calendarTitle }}</h2>
      <div class="segmented">
        <button :class="{ selected: calendarView === 'month' }" @click="showCalendar('month')">
          月</button
        ><button :class="{ selected: calendarView === 'week' }" @click="showCalendar('week')">
          周</button
        ><button :class="{ selected: calendarView === 'list' }" @click="showCalendar('list')">
          日程
        </button>
      </div>
    </div>
    <div
      v-if="calendarView !== 'list'"
      :class="['calendar-grid', { 'week-view': calendarView === 'week' }]"
    >
      <div
        v-for="day in ['一', '二', '三', '四', '五', '六', '日']"
        :key="day"
        class="calendar-weekday"
      >
        周{{ day }}
      </div>
      <div
        v-for="day in days"
        :key="day"
        :class="[
          'calendar-day',
          {
            today: day === calendarDay(new Date()),
            outside: calendarView === 'month' && day.slice(0, 7) !== calendarAnchor.slice(0, 7),
          },
        ]"
      >
        <strong>{{ Number(day.slice(-2)) }}</strong>
        <button
          v-for="entry in groupedEntries.filter((value) => value.date === day)"
          :key="entry.key"
          class="calendar-entry event-card"
          :class="{ complete: eventComplete(entry) }"
          @click="selectedEvent = entry"
        >
          <img
            v-if="image(entry.poster_path, 'w92')"
            :src="image(entry.poster_path, 'w92')"
            alt=""
          />
          <span class="event-info"
            ><b>{{ entry.title }}</b
            ><small
              >{{ entry.media_type === 'tv' ? 'S' + entry.season + ' · ' : ''
              }}{{ calendarEpisodes(entry.entries) }}</small
            ><small>{{
              eventComplete(entry) ? '已入库' : eventState(entry.entries[0])
            }}</small></span
          >
        </button>
      </div>
    </div>
    <div v-else class="calendar-agenda panel">
      <section v-for="day in agendaDays" :key="day">
        <h3>{{ day }}</h3>
        <button
          v-for="entry in groupedEntries.filter((value) => value.date === day)"
          :key="entry.key"
          class="agenda-entry"
          @click="selectedEvent = entry"
        >
          <img
            v-if="image(entry.poster_path, 'w92')"
            :src="image(entry.poster_path, 'w92')"
            alt=""
          /><strong>{{ entry.title }}</strong
          ><span
            >{{ entry.media_type === 'tv' ? 'S' + entry.season + ' · ' : ''
            }}{{ calendarEpisodes(entry.entries) }}</span
          ><span>{{ eventComplete(entry) ? '已入库' : eventState(entry.entries[0]) }}</span>
        </button>
      </section>
      <p v-if="!agendaDays.length && !loading" class="notice">没有更新安排。</p>
    </div>
  </template>
  <template v-else
    ><div class="stat-row">
      <div class="stat">
        <span>全部订阅</span><strong>{{ subs.length }}</strong>
      </div>
      <div class="stat">
        <span>正在追更</span><strong>{{ subs.filter((s) => s.state === 'active').length }}</strong>
      </div>
      <div class="stat">
        <span>已暂停</span><strong>{{ subs.filter((s) => s.state === 'paused').length }}</strong>
      </div>
    </div>
    <div class="toolbar">
      <input v-model="filterText" placeholder="搜索订阅" aria-label="搜索订阅" />
      <select v-model="filterKind">
        <option value="all">全部类型</option>
        <option value="movie">电影</option>
        <option value="tv">剧集</option>
      </select>
      <select v-model="filterState">
        <option value="all">全部状态</option>
        <option value="active">订阅中</option>
        <option value="paused">已暂停</option>
        <option value="completed">已完成</option>
      </select>
      <select v-model="sortBy">
        <option value="created">最近添加</option>
        <option value="name">按名称</option>
        <option value="missing">缺集最多</option>
      </select>
      <button @click="load">刷新</button
      ><span class="hint push-right">{{ filteredSubs.length }} 个订阅</span>
    </div>
    <div class="subscription-grid">
      <article v-for="s in filteredSubs" :key="s.id" class="subscription-card">
        <img
          v-if="image(s.media?.poster_path, 'w185')"
          :src="image(s.media.poster_path, 'w185')"
          :alt="s.media.title"
        />
        <div>
          <div class="section-heading">
            <h3>{{ s.media.title }}</h3>
            <span :class="['badge', 'subscription-state', s.state]">{{ label(s.state) }}</span>
          </div>
          <p class="muted">
            {{ s.media.media_type === 'tv' ? '第 ' + s.season + ' 季' : '电影' }} ·
            {{
              s.media.media_type === 'tv'
                ? s.target_scope === 'season' || !s.episodes?.length
                  ? '从第 ' + (s.start_episode || 1) + ' 集开始 · 整季追更'
                  : s.episodes.length + ' 集（历史范围）'
                : '整部订阅'
            }}
          </p>
          <p v-if="s.download_path" class="hint path-cell">下载目录：{{ s.download_path }}</p>
          <div class="subscription-progress">
            <progress :value="percent(s)" max="100"></progress
            ><span
              >已入库 {{ s.in_library_episodes?.length || 0 }} / {{ totalEpisodes(s) || '待定'
              }}<b v-if="totalEpisodes(s)"> · {{ percent(s) }}%</b></span
            >
          </div>
          <div class="tags">
            <span>已入库 {{ s.in_library_episodes?.length || 0 }}</span
            ><span
              >下载中
              {{
                (s.reserved_episodes || []).filter(
                  (e: number) => !(s.pending_import_episodes || []).includes(e),
                ).length
              }}</span
            ><span>下载完成待整理 {{ s.pending_import_episodes?.length || 0 }}</span
            ><span
              >已播缺集
              {{
                (s.missing_episodes || []).filter(
                  (e: number) =>
                    !(s.unreleased_episodes || []).includes(e) &&
                    !(s.unknown_airdate_episodes || []).includes(e),
                ).length
              }}</span
            ><span>未播 {{ s.unreleased_episodes?.length || 0 }}</span
            ><span>播出日期待确认 {{ s.unknown_airdate_episodes?.length || 0 }}</span>
          </div>
          <p class="hint">
            下次检查：{{ date(s.next_check) }}<br />上次检查：{{ date(s.last_check) }}<br />{{
              s.last_reason
            }}
          </p>
          <div class="button-row">
            <button class="small primary" @click="check(s)">立即检查</button
            ><button class="small" @click="begin(s)">编辑</button
            ><button class="small" @click="state(s)">
              {{ s.state === 'paused' ? '恢复' : '暂停' }}</button
            ><button class="small danger" @click="remove(s)">删除</button>
          </div>
        </div>
      </article>
    </div>
    <div v-if="!subs.length && !loading" class="empty-state">
      <span>◉</span>
      <h3>把下一部想看的作品加入这里</h3>
      <p>可以从发现页选择作品，或在此创建订阅。</p>
    </div></template
  >
  <div v-if="editing && !multiSeason" class="modal-backdrop">
    <section class="modal wide" role="dialog" aria-modal="true" aria-label="订阅设置">
      <div class="section-heading">
        <h2>{{ editing.id ? '编辑订阅' : '新建订阅' }}</h2>
        <button class="icon-button" @click="editing = null" aria-label="关闭">×</button>
      </div>
      <div v-if="!editing.media">
        <form class="inline-form" @submit.prevent="find">
          <input v-model="lookup" placeholder="搜索要订阅的作品" required /><button class="primary">
            搜索
          </button>
        </form>
        <div class="lookup-list">
          <button v-for="m in lookupResults" :key="m.media_type + m.id" @click="pick(m)">
            {{ m.title }} <span>{{ m.year }} · {{ label(m.media_type) }}</span>
          </button>
        </div>
      </div>
      <template v-else>
        <p class="subscription-title">
          <strong>{{ editing.media.title }}</strong
          ><span>{{ editing.media.year }}</span>
        </p>
        <div class="tabs">
          <button :class="{ selected: editingTab === 'basic' }" @click="editingTab = 'basic'">
            基础</button
          ><button
            :class="{ selected: editingTab === 'advanced' }"
            @click="editingTab = 'advanced'"
          >
            进阶
          </button>
        </div>
        <div v-show="editingTab === 'basic'">
          <div v-if="editing.media.media_type === 'tv'" class="form-grid three-columns">
            <label
              >季号<select
                :value="editing.season"
                @change="setSeason(Number(($event.target as HTMLSelectElement).value))"
              >
                <option
                  v-for="season in (editing.media.seasons || []).filter(
                    (value: any) => value.season_number > 0,
                  )"
                  :key="season.season_number"
                  :value="season.season_number"
                >
                  {{ season.name || '第 ' + season.season_number + ' 季' }}
                </option>
                <option v-if="!editing.media.seasons?.length" :value="editing.season">
                  第 {{ editing.season }} 季
                </option>
              </select></label
            >
          </div>
          <div class="form-grid three-columns">
            <label>搜索关键词<input v-model.trim="editing.keyword" /></label>
            <label v-if="editing.media.media_type === 'tv'"
              >总集数<input
                v-model.number="editing.total_episodes"
                type="number"
                min="0"
                step="1"
                placeholder="自动"
            /></label>
            <label v-if="editing.media.media_type === 'tv'"
              >开始集数<input v-model.number="editing.start_episode" type="number" min="1" step="1"
            /></label>
          </div>
          <div class="form-grid three-columns">
            <label
              >质量<select v-model="editing.policy.source">
                <option value="">默认</option>
                <option value="WEB-DL">WEB-DL</option>
                <option value="WEBRip">WEBRip</option>
                <option value="BluRay">BluRay</option>
                <option value="BDRip">BDRip</option>
                <option value="HDTV">HDTV</option>
                <option value="REMUX">REMUX</option>
                <option value="DVD">DVD</option>
              </select></label
            >
            <label
              >分辨率<select v-model="editing.policy.resolution">
                <option value="">默认</option>
                <option>2160p</option>
                <option>1080p</option>
                <option>720p</option>
              </select></label
            >
            <label
              >特效<select v-model="editing.policy.hdr">
                <option value="">默认</option>
                <option value="sdr">SDR</option>
                <option value="hdr10">HDR10</option>
                <option value="hdr10plus">HDR10+</option>
                <option value="dolby_vision">Dolby Vision</option>
              </select></label
            >
          </div>
          <div class="subscription-sites">
            <label>订阅站点</label>
            <div class="site-selection">
              <span v-for="site in selectedSites" :key="site.id" class="site-chip"
                >{{ site.name
                }}<button
                  class="icon-button"
                  :aria-label="'取消 ' + site.name"
                  @click="toggleSite(site.id)"
                >
                  ×
                </button></span
              ><button
                class="site-dropdown"
                :aria-expanded="sitesOpen"
                @click="sitesOpen = !sitesOpen"
              >
                {{ selectedSites.length ? '⌄' : '默认　⌄' }}</button
              ><button
                v-if="selectedSites.length"
                class="icon-button"
                aria-label="清除站点"
                @click="editing.site_ids = []"
              >
                ×
              </button>
            </div>
            <div v-if="sitesOpen" class="site-options">
              <label v-for="site in siteOptions" :key="site.id" class="check"
                ><input
                  type="checkbox"
                  :checked="editing.site_ids.includes(site.id)"
                  @change="toggleSite(site.id)"
                />{{ site.name }}</label
              >
              <p v-if="!siteOptions.length" class="hint">没有启用的站点。</p>
            </div>
          </div>
          <DownloadTarget
            v-model:downloader="editing.downloader_id"
            v-model:download-path="editing.download_path"
          />
        </div>
        <div v-show="editingTab === 'advanced'" class="subscription-advanced">
          <Policy :model="editing.policy" advanced />
          <label
            >状态<select v-model="editing.state">
              <option value="active">启用</option>
              <option value="paused">暂停</option>
            </select></label
          >
          <h3>更新时间与检查周期</h3>
          <Schedule :model="editing.schedule" />
          <template v-if="editing.media.media_type === 'tv'">
            <h3>单集设置</h3>
            <div class="form-grid">
              <label>排除集数<input v-model="excluded" /></label
              ><label>允许提前下载的集数<input v-model="allowEarly" /></label>
            </div>
            <label
              >单集日期覆盖<textarea
                v-model="dates"
                rows="3"
                placeholder="17=2026-10-16"
              ></textarea>
            </label>
            <MappingRules
              :model="editing.mappings"
              :media-id="editing.media.media_type + ':' + editing.media.id"
              :season="editing.season"
            />
            <label
              v-if="editing.id && editing.target_scope !== 'season' && editing.episodes?.length"
              class="check"
              ><input v-model="convertLegacy" type="checkbox" />从开始集数起整季订阅（原范围：{{
                editing.episodes.join('、')
              }}）</label
            >
          </template>
        </div>
        <div class="button-row">
          <button
            v-if="
              !editing.id && editing.media.media_type === 'tv' && editing.media.seasons?.length > 1
            "
            @click="openMulti"
          >
            选择多个季度订阅
          </button>
          <button @click="editing = null">取消</button
          ><button class="primary" :disabled="ui.busy > 0" @click="save">保存</button>
        </div></template
      >
    </section>
  </div>
  <div v-if="selectedEvent" class="modal-backdrop" @click.self="selectedEvent = null">
    <section class="modal" role="dialog" aria-modal="true" aria-label="更新详情">
      <header class="section-heading">
        <h2>更新详情</h2>
        <button class="icon-button" aria-label="关闭更新详情" @click="selectedEvent = null">
          ×
        </button>
      </header>
      <div class="event-detail">
        <img
          v-if="image(selectedEvent.poster_path, 'w185')"
          :src="image(selectedEvent.poster_path, 'w185')"
          alt=""
        />
        <div>
          <h3>{{ selectedEvent.title }}</h3>
          <p>{{ selectedEvent.date }}</p>
          <p>
            {{ selectedEvent.media_type === 'tv' ? '第 ' + selectedEvent.season + ' 季 · ' : ''
            }}{{ calendarEpisodes(selectedEvent.entries) }}
          </p>
          <p>{{ eventSource(selectedEvent) }}</p>
          <p v-if="eventSubscription">
            已入库 {{ eventSubscription.in_library_episodes?.length || 0 }} /
            {{ totalEpisodes(eventSubscription) || '待定' }}
          </p>
        </div>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>集数</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(entry, index) in selectedEvent.entries" :key="index">
              <td>
                {{
                  entry.confirmed && entry.episode
                    ? 'E' + entry.episode
                    : entry.media_type === 'movie'
                      ? '电影'
                      : '更新'
                }}
              </td>
              <td>{{ eventState(entry) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <footer class="button-row">
        <button @click="selectedEvent = null">关闭</button
        ><button class="primary" :disabled="!eventSubscription" @click="editEvent">编辑订阅</button>
      </footer>
    </section>
  </div>
  <MultiSeasonSubscribe
    v-if="multiSeason && editing"
    :template="multiTemplate"
    :existing="subs"
    @changed="load"
    @close="closeMulti"
  />
</template>

<style scoped>
.subscription-progress {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  color: var(--muted);
  margin: 12px 0;
}
.subscription-progress progress {
  width: 40%;
  height: 7px;
  accent-color: var(--accent, #7c83fd);
}
.toolbar input {
  max-width: 230px;
}

.calendar-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}
.calendar-toolbar h2 {
  margin: 0;
  font-size: 19px;
}
.calendar-day.outside {
  background: #fafbfc;
}
.calendar-day.outside > strong {
  opacity: 0.45;
}
.event-card {
  justify-content: flex-start;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px;
}
.event-card > img {
  width: 36px;
  height: 54px;
  flex: none;
  border-radius: 3px;
  object-fit: cover;
}
.event-card > .event-info {
  min-width: 0;
}
.event-card.complete {
  background: #eff9f1;
  border-left-color: #68a979;
}
.event-card.complete small {
  color: #3d7951;
}
.calendar-agenda section {
  border-bottom: 1px solid var(--line);
  padding: 8px 0 16px;
}
.calendar-agenda h3 {
  margin: 5px 0 8px;
  font-size: 14px;
}
.agenda-entry {
  display: flex;
  align-items: center;
  gap: 16px;
  width: 100%;
  background: transparent;
  border: 0;
  text-align: left;
  padding: 10px 4px;
}
.agenda-entry:hover {
  background: var(--accent-soft);
}
.agenda-entry > img {
  width: 30px;
  height: 44px;
  border-radius: 3px;
  object-fit: cover;
}
.agenda-entry > strong {
  flex: 1;
}
.agenda-entry > span {
  color: var(--muted);
  font-size: 12px;
}
.subscription-title {
  display: flex;
  gap: 12px;
  align-items: baseline;
  margin: 0 0 20px;
}
.subscription-title > span {
  color: var(--muted);
  font-size: 13px;
}
.three-columns {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
.subscription-sites {
  position: relative;
  margin: 16px 0;
  font-size: 12px;
}
.site-selection {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 7px;
  min-height: 42px;
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 5px 8px;
}
.site-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border-radius: 6px;
  padding: 2px 5px 2px 9px;
  background: var(--accent-soft);
  color: var(--accent);
}
.site-chip .icon-button {
  padding: 2px 5px;
  font-size: 17px;
}
.site-dropdown {
  justify-content: flex-start;
  flex: 1;
  text-align: left;
  padding: 4px;
  background: none;
  border: 0;
  color: var(--muted);
}
.site-options {
  position: absolute;
  top: 100%;
  left: 0;
  width: 100%;
  max-height: 210px;
  overflow: auto;
  z-index: 10;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: 0 8px 20px #0002;
  padding: 10px;
}
.site-options > label {
  padding: 7px;
}
.subscription-advanced > h3 {
  border-top: 1px solid var(--line);
  padding-top: 20px;
  margin-top: 25px;
}
.event-detail {
  display: flex;
  align-items: flex-start;
  gap: 20px;
  margin: 15px 0;
}
.event-detail > img {
  width: 100px;
  border-radius: 7px;
}
.event-detail h3 {
  margin-top: 0;
}
.modal footer {
  margin-top: 20px;
  justify-content: flex-end;
}
@media (max-width: 700px) {
  .three-columns {
    grid-template-columns: 1fr;
  }
  .event-card {
    flex-direction: column;
    align-items: flex-start;
  }
  .event-card > img {
    width: 100%;
    height: auto;
    aspect-ratio: 2/3;
  }
  .calendar-toolbar h2 {
    order: -1;
    width: 100%;
    text-align: center;
  }
  .agenda-entry {
    gap: 9px;
    flex-wrap: wrap;
  }
}

.stat-row {
  gap: 16px;
  margin-bottom: 22px;
}
.stat {
  padding: 19px 23px;
  border-color: var(--line);
  border-radius: 12px;
  background: var(--surface);
  box-shadow: var(--shadow);
}
.stat > span {
  font-size: 12px;
  font-weight: 500;
  color: #657c89;
}
.stat > strong {
  font-size: 28px;
  font-weight: 650;
  letter-spacing: -0.7px;
  color: #213e4b;
  font-variant-numeric: tabular-nums;
}
.stat:nth-child(2) > strong {
  color: var(--accent);
}
.toolbar {
  gap: 11px;
  margin-bottom: 22px;
  flex-wrap: wrap;
}
.toolbar > select {
  min-width: 115px;
}
.subscription-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
}
.subscription-card {
  align-items: flex-start;
  gap: 19px;
  padding: 22px;
  border-color: var(--line);
  border-radius: 12px;
  background: var(--surface);
  box-shadow: var(--shadow);
}
.subscription-card > img {
  display: block;
  width: 86px;
  height: 129px;
  border-radius: 8px;
  box-shadow: 0 3px 8px #172b3a0d;
}
.subscription-card .section-heading {
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 8px;
}
.subscription-card h3 {
  margin: 0;
  color: #172b3a;
  font-size: 16px;
  line-height: 1.5;
  font-weight: 650;
}
.subscription-state {
  flex: none;
  padding: 4px 7px;
  border: 0;
  border-radius: 5px;
  background: #f0f4f6;
  color: #70808d;
  font-size: 10px;
  font-weight: 500;
}
.subscription-state.active {
  background: var(--accent-soft);
  color: var(--accent);
}
.subscription-state.completed {
  background: #edf6ef;
  color: #387d54;
}
.subscription-state.paused {
  background: #fff5e6;
  color: #9a7429;
}
.subscription-card .muted {
  margin: 0 0 10px;
  color: #6c818e;
  font-size: 12px;
  line-height: 1.7;
}
.subscription-progress {
  flex-direction: column;
  align-items: flex-start;
  gap: 7px;
  margin: 15px 0 12px;
}
.subscription-progress progress {
  width: 100%;
  height: 5px;
  border: 0;
  border-radius: 4px;
  accent-color: var(--accent);
  overflow: hidden;
}
.subscription-progress progress::-webkit-progress-bar {
  background: #eaf0f2;
  border-radius: 4px;
}
.subscription-progress progress::-webkit-progress-value {
  background: var(--accent);
  border-radius: 4px;
}
.subscription-progress progress::-moz-progress-bar {
  background: var(--accent);
  border-radius: 4px;
}
.subscription-progress > span {
  font-size: 11px;
  color: #7b8b96;
}
.subscription-progress b {
  color: var(--accent);
  font-weight: 600;
}
.subscription-card .tags {
  gap: 5px;
  margin: 11px 0;
}
.subscription-card .tags > span {
  padding: 3px 6px;
  border: 0;
  border-radius: 5px;
  background: #f2f6f7;
  color: #637986;
  font-size: 10px;
}
.subscription-card .hint {
  margin: 10px 0 14px;
  color: #7a8d99;
  font-size: 11px;
  line-height: 1.8;
}
.subscription-card .button-row {
  gap: 6px;
  margin-top: 15px;
  padding-top: 14px;
  border-top: 1px solid var(--line);
}
.subscription-card .small {
  min-height: 30px;
  padding: 6px 9px;
  border-radius: 6px;
  font-size: 11px;
}
.calendar-toolbar {
  margin-bottom: 20px;
}
.calendar-toolbar h2 {
  color: #263e4c;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.3px;
}
.calendar-grid {
  border-color: var(--line);
  border-radius: 12px;
  background: var(--surface);
  box-shadow: var(--shadow);
}
.calendar-weekday {
  padding: 13px;
  border-color: var(--line);
  background: #f4f7f9;
  color: #748793;
  font-size: 11px;
  font-weight: 550;
}
.calendar-day {
  min-height: 150px;
  padding: 12px 10px;
  border-color: var(--line);
}
.calendar-day > strong {
  color: #738692;
  font-size: 11px;
}
.calendar-day.today {
  background: #f2faf8;
}
.calendar-day.today > strong {
  width: 25px;
  height: 25px;
  background: var(--accent);
  font-size: 11px;
  font-weight: 600;
}
.calendar-day.outside {
  background: #fafcfc;
}
.event-card {
  gap: 8px;
  padding: 7px;
  border-left: 3px solid #71b7ab;
  border-radius: 6px;
  background: #edf7f4;
  color: #2b5b58;
}
.event-card:hover {
  background: #e1f2eb;
}
.event-card > img {
  width: 32px;
  height: 48px;
  border-radius: 4px;
}
.event-card strong {
  color: #2e5c59;
  font-size: 11px;
  line-height: 1.5;
}
.event-card small {
  color: #71918a;
  font-size: 10px;
  line-height: 1.6;
}
.event-card.complete {
  background: #f1f5f3;
  border-left-color: #9cbbb0;
}
.calendar-agenda {
  padding: 8px 22px;
  border-radius: 12px;
}
.calendar-agenda section {
  padding: 16px 0;
}
.calendar-agenda section:last-child {
  border-bottom: 0;
}
.calendar-agenda h3 {
  margin: 0 0 10px;
  color: #657c89;
  font-size: 12px;
  font-weight: 600;
}
.agenda-entry {
  padding: 10px;
  border-radius: 8px;
}
.agenda-entry > img {
  width: 36px;
  height: 54px;
  border-radius: 5px;
}
.agenda-entry > strong {
  color: #28414f;
  font-size: 13px;
}
.site-options {
  border-radius: 10px;
  box-shadow: 0 10px 25px #172b3a16;
}
.site-options > label {
  min-height: 34px;
  border-radius: 6px;
}
.site-options > label:hover {
  background: var(--accent-soft);
}
.event-detail > img {
  border-radius: 9px;
  box-shadow: 0 3px 10px #172b3a12;
}
@media (max-width: 1200px) {
  .subscription-grid {
    grid-template-columns: 1fr;
  }
  .subscription-card > img {
    width: 94px;
    height: 141px;
  }
}
@media (max-width: 700px) {
  .stat-row {
    gap: 10px;
  }
  .stat {
    padding: 15px;
  }
  .stat > strong {
    font-size: 24px;
  }
  .calendar-day {
    padding: 8px 5px;
  }
  .event-card {
    padding: 5px;
    gap: 6px;
  }
  .event-card > img {
    width: 100%;
    height: auto;
    aspect-ratio: 2 / 3;
  }
}
@media (max-width: 520px) {
  .subscription-card {
    gap: 14px;
    padding: 17px;
  }
  .subscription-card > img {
    width: 67px;
    height: 100px;
  }
  .subscription-card h3 {
    font-size: 15px;
  }
  .subscription-card .small {
    padding: 6px 8px;
  }
  .stat {
    flex-direction: column;
    align-items: flex-start;
    gap: 9px;
    padding: 14px;
  }
  .stat > span {
    font-size: 11px;
  }
  .calendar-agenda {
    padding: 4px 13px;
  }
}
</style>

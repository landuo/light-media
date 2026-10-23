<script setup lang="ts">
import { ref, onMounted, computed, watch, useId } from 'vue';
import { VTextarea } from 'vuetify/components/VTextarea';
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
import AppIcon from './AppIcon.vue';
import Schedule from './Schedule.vue';
import MappingRules from './MappingRules.vue';
import MultiSeasonSubscribe from './MultiSeasonSubscribe.vue';
import SubscriptionField from './SubscriptionField.vue';
import SubscriptionFilters from './SubscriptionFilters.vue';
import { subscriptionFilterValue, setSubscriptionFilter } from '../features/subscription-filters';
import { downloadDestination, downloadDirectoryPaths } from '../features/download-directories';
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
  siteQuery = ref(''),
  activeSite = ref(-1),
  selectedEvent = ref<any>(null),
  loading = ref(false);
const calendarView = ref<CalendarView>('month'),
  calendarAnchor = ref(calendarDay(new Date()));
const siteOptions = computed(() => (ui.settings?.sites || []).filter((site: any) => site.enabled));
const visibleSiteOptions = computed(() =>
  siteOptions.value.filter((site: any) =>
    String(site.name || site.id)
      .toLocaleLowerCase()
      .includes(siteQuery.value.toLocaleLowerCase()),
  ),
);
const downloaderOptions = computed(() =>
  (ui.settings?.downloaders || []).filter((downloader: any) => downloader.enabled !== false),
);
const downloadPaths = computed(() => downloadDirectoryPaths(ui.settings?.directories || []));
const editorID = 'subscription-editor-' + useId();
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
const typedView = computed(() =>
  ['movie', 'tv'].includes(new URLSearchParams(props.view.split('?')[1]).get('type') || ''),
);
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
  siteQuery.value = '';
  activeSite.value = -1;
}
function closeSites() {
  sitesOpen.value = false;
  siteQuery.value = '';
  activeSite.value = -1;
}
function siteKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    closeSites();
    event.stopPropagation();
    return;
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    sitesOpen.value = true;
    const count = visibleSiteOptions.value.length;
    if (count)
      activeSite.value =
        activeSite.value < 0
          ? event.key === 'ArrowDown'
            ? 0
            : count - 1
          : (activeSite.value + (event.key === 'ArrowDown' ? 1 : -1) + count) % count;
  } else if (event.key === 'Enter' && sitesOpen.value) {
    event.preventDefault();
    const site = visibleSiteOptions.value[Math.max(activeSite.value, 0)];
    if (site) toggleSite(site.id);
  }
}
function policyCSV(field: string, event: Event) {
  editing.value.policy[field] = (event.target as HTMLInputElement).value
    .split(/[,，]+/)
    .map((value) => value.trim())
    .filter(Boolean);
}
function policySize(field: string, event: Event) {
  const value = Number((event.target as HTMLInputElement).value);
  editing.value.policy[field] =
    Number.isFinite(value) && value > 0 ? Math.round(value * 1073741824) : 0;
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
        custom_words: '',
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
  siteQuery.value = '';
  activeSite.value = -1;
  convertLegacy.value = false;
  editing.value.start_episode ||= 1;
  editing.value.download_path ||= '';
  editing.value.site_ids ||= [];
  editing.value.custom_words ??= '';
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
  if (!confirm('删除《' + s.media.title + '》的订阅？已下载文件会保留。')) return false;
  const r = await action(() => api('/subscriptions/' + s.id, undefined, 'DELETE'), '订阅已删除');
  if (r === undefined) return false;
  await load();
  return true;
}
async function cancelSubscription() {
  const current = editing.value;
  if ((await remove(current)) && editing.value === current) editing.value = null;
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
  <div v-if="!isCalendar" class="subscription-actions">
    <button
      class="primary subscription-add"
      aria-label="新建订阅"
      title="新建订阅"
      @click="begin()"
    >
      <AppIcon name="plus" />
    </button>
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
          :aria-label="entry.title + ' · ' + calendarEpisodes(entry.entries)"
          :title="entry.title + ' · ' + calendarEpisodes(entry.entries)"
          @click="selectedEvent = entry"
        >
          <img
            v-if="image(entry.poster_path, 'w92')"
            :src="image(entry.poster_path, 'w92')"
            alt=""
          />
          <span class="calendar-episode-overlay">{{ calendarEpisodes(entry.entries) }}</span>
          <span v-if="eventComplete(entry)" class="calendar-library-check"
            ><AppIcon name="check"
          /></span>
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
  <template v-else>
    <div v-if="!typedView" class="toolbar">
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
      <article
        v-for="s in filteredSubs"
        :key="s.id"
        class="subscription-card"
        :class="{ paused: s.state === 'paused' }"
      >
        <img
          v-if="image(s.media?.backdrop_path || s.media?.poster_path, 'w780')"
          class="subscription-backdrop"
          :src="image(s.media.backdrop_path || s.media.poster_path, 'w780')"
          alt=""
        />
        <button
          class="subscription-card-content"
          :aria-label="'编辑订阅 ' + s.media.title"
          @click="begin(s)"
        >
          <div class="subscription-heading">
            <img
              v-if="image(s.media?.poster_path, 'w185')"
              class="subscription-poster"
              :src="image(s.media.poster_path, 'w185')"
              :alt="s.media.title"
            />
            <div class="subscription-name">
              <span>{{ s.media.year }}</span>
              <h3>
                {{ s.media.title
                }}{{ s.media.media_type === 'tv' ? ' 第 ' + s.season + ' 季' : '' }}
              </h3>
            </div>
          </div>
          <div class="subscription-progress-text">
            <AppIcon name="download" />
            <span
              >已入库 {{ s.in_library_episodes?.length || 0 }} /
              {{ totalEpisodes(s) || '待定' }}</span
            >
          </div>
          <span :class="['subscription-state', s.state]">{{ label(s.state) }}</span>
        </button>
        <details class="subscription-menu">
          <summary :aria-label="s.media.title + ' 更多操作'" title="更多操作">
            <AppIcon name="more" />
          </summary>
          <div class="subscription-menu-panel">
            <div class="subscription-menu-actions">
              <button @click="check(s)"><AppIcon name="refresh" />立即检查</button>
              <button @click="begin(s)"><AppIcon name="edit" />编辑</button>
              <button @click="state(s)">
                <AppIcon :name="s.state === 'paused' ? 'play' : 'pause'" />{{
                  s.state === 'paused' ? '恢复' : '暂停'
                }}
              </button>
              <button class="danger" @click="remove(s)"><AppIcon name="trash" />删除</button>
            </div>
            <div class="subscription-metadata">
              <p>
                {{
                  s.media.media_type === 'tv'
                    ? s.target_scope === 'season' || !s.episodes?.length
                      ? '从第 ' + (s.start_episode || 1) + ' 集开始 · 整季追更'
                      : s.episodes.length + ' 集（历史范围）'
                    : '整部订阅'
                }}
              </p>
              <p v-if="s.download_path" class="path-cell">下载目录：{{ s.download_path }}</p>
              <dl>
                <div>
                  <dt>已入库</dt>
                  <dd>{{ s.in_library_episodes?.length || 0 }}</dd>
                </div>
                <div>
                  <dt>下载中</dt>
                  <dd>
                    {{
                      (s.reserved_episodes || []).filter(
                        (e: number) => !(s.pending_import_episodes || []).includes(e),
                      ).length
                    }}
                  </dd>
                </div>
                <div>
                  <dt>下载完成待整理</dt>
                  <dd>{{ s.pending_import_episodes?.length || 0 }}</dd>
                </div>
                <div>
                  <dt>已播缺集</dt>
                  <dd>
                    {{
                      (s.missing_episodes || []).filter(
                        (e: number) =>
                          !(s.unreleased_episodes || []).includes(e) &&
                          !(s.unknown_airdate_episodes || []).includes(e),
                      ).length
                    }}
                  </dd>
                </div>
                <div>
                  <dt>未播</dt>
                  <dd>{{ s.unreleased_episodes?.length || 0 }}</dd>
                </div>
                <div>
                  <dt>播出日期待确认</dt>
                  <dd>{{ s.unknown_airdate_episodes?.length || 0 }}</dd>
                </div>
              </dl>
              <p>
                下次检查：{{ date(s.next_check) }}<br />上次检查：{{ date(s.last_check) }}<br />{{
                  s.last_reason
                }}
              </p>
            </div>
          </div>
        </details>
        <progress
          class="subscription-progress"
          :value="percent(s)"
          max="100"
          :aria-label="s.media.title + ' 入库进度'"
        ></progress>
      </article>
    </div>
    <div v-if="!subs.length && !loading" class="empty-state">
      <span>◉</span>
      <h3>把下一部想看的作品加入这里</h3>
      <p>可以从发现页选择作品，或在此创建订阅。</p>
    </div></template
  >
  <div
    v-if="editing && !multiSeason"
    class="modal-backdrop subscription-editor-backdrop"
    @click.self="editing = null"
  >
    <section
      class="modal subscription-dialog"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="editorID + '-title'"
      @keydown.esc="editing = null"
    >
      <header class="subscription-dialog-header">
        <div class="subscription-dialog-title">
          <AppIcon name="clipboard" />
          <div>
            <h2 :id="editorID + '-title'">{{ editing.id ? '编辑订阅' : '新建订阅' }}</h2>
            <p v-if="editing.media">
              {{ editing.media.title
              }}{{ editing.media.media_type === 'tv' ? ' 第 ' + editing.season + ' 季' : '' }}
            </p>
          </div>
        </div>
        <button class="icon-button" @click="editing = null" aria-label="关闭">
          <AppIcon name="close" />
        </button>
      </header>
      <div class="subscription-dialog-body">
        <div v-if="!editing.media">
          <form class="inline-form" @submit.prevent="find">
            <input v-model="lookup" placeholder="搜索要订阅的作品" required /><button
              class="primary"
            >
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
          <div class="tabs subscription-edit-tabs" role="tablist" aria-label="订阅设置">
            <button
              :id="editorID + '-basic-tab'"
              role="tab"
              :aria-selected="editingTab === 'basic'"
              :aria-controls="editorID + '-basic'"
              :class="{ selected: editingTab === 'basic' }"
              @click="
                editingTab = 'basic';
                closeSites();
              "
            >
              基础</button
            ><button
              :id="editorID + '-advanced-tab'"
              role="tab"
              :aria-selected="editingTab === 'advanced'"
              :aria-controls="editorID + '-advanced'"
              :class="{ selected: editingTab === 'advanced' }"
              @click="
                editingTab = 'advanced';
                closeSites();
              "
            >
              进阶
            </button>
          </div>
          <div
            v-show="editingTab === 'basic'"
            :id="editorID + '-basic'"
            role="tabpanel"
            :aria-labelledby="editorID + '-basic-tab'"
            class="subscription-basic"
          >
            <div class="subscription-edit-grid three-columns">
              <SubscriptionField label="搜索关键词" icon="search" hint="指定搜索站点时使用的关键词">
                <input v-model.trim="editing.keyword" aria-label="搜索关键词" />
              </SubscriptionField>
              <SubscriptionField
                v-if="editing.media.media_type === 'tv'"
                label="总集数"
                icon="playlist"
                hint="剧集总集数"
              >
                <input
                  v-model.number="editing.total_episodes"
                  aria-label="总集数"
                  type="number"
                  min="0"
                  step="1"
                  placeholder="自动"
                />
              </SubscriptionField>
              <SubscriptionField
                v-if="editing.media.media_type === 'tv'"
                label="开始集数"
                icon="play"
                hint="开始订阅集数"
              >
                <input
                  v-model.number="editing.start_episode"
                  aria-label="开始集数"
                  type="number"
                  min="1"
                  step="1"
                />
              </SubscriptionField>
            </div>
            <SubscriptionFilters v-model="editing.policy" />
            <div
              class="subscription-sites"
              @focusout="
                if (!($event.currentTarget as HTMLElement).contains($event.relatedTarget as Node))
                  closeSites();
              "
            >
              <span class="subscription-sites-label">订阅站点</span>
              <div class="site-selection">
                <AppIcon name="globe" />
                <div class="site-values">
                  <span v-for="site in selectedSites" :key="site.id" class="site-chip">{{
                    site.name
                  }}</span>
                  <input
                    v-model="siteQuery"
                    class="site-query"
                    role="combobox"
                    aria-label="搜索订阅站点"
                    autocomplete="off"
                    :placeholder="selectedSites.length ? '' : '默认'"
                    :aria-expanded="sitesOpen"
                    :aria-controls="editorID + '-sites'"
                    :aria-activedescendant="
                      activeSite >= 0 ? editorID + '-site-' + activeSite : undefined
                    "
                    @focus="sitesOpen = true"
                    @input="
                      sitesOpen = true;
                      activeSite = 0;
                    "
                    @keydown="siteKeydown"
                  />
                </div>
                <button
                  class="icon-button site-dropdown"
                  aria-label="订阅站点选择"
                  :aria-expanded="sitesOpen"
                  @click="sitesOpen ? closeSites() : (sitesOpen = true)"
                >
                  <AppIcon :name="sitesOpen ? 'chevron-up' : 'chevron-down'" /></button
                ><button
                  v-if="selectedSites.length"
                  class="icon-button"
                  aria-label="清除站点"
                  @click="
                    editing.site_ids = [];
                    siteQuery = '';
                    activeSite = -1;
                  "
                >
                  <AppIcon name="close" />
                </button>
              </div>
              <small>订阅的站点范围，不选使用系统设置</small>
              <div
                v-if="sitesOpen"
                :id="editorID + '-sites'"
                class="site-options"
                role="listbox"
                aria-label="订阅站点"
                aria-multiselectable="true"
                @keydown.esc.stop="closeSites"
              >
                <button
                  v-for="(site, index) in visibleSiteOptions"
                  :id="editorID + '-site-' + index"
                  :key="site.id"
                  role="option"
                  :aria-selected="editing.site_ids.includes(site.id)"
                  :class="{ 'site-option-active': activeSite === index }"
                  @click="toggleSite(site.id)"
                >
                  <span
                    class="site-option-check"
                    :class="{ checked: editing.site_ids.includes(site.id) }"
                  >
                    <AppIcon v-if="editing.site_ids.includes(site.id)" name="check" />
                  </span>
                  {{ site.name }}
                </button>
                <p v-if="!visibleSiteOptions.length" class="hint">
                  {{ siteOptions.length ? '没有匹配的站点。' : '没有启用的站点。' }}
                </p>
              </div>
            </div>
            <div class="subscription-edit-grid">
              <SubscriptionField label="下载器" icon="download" hint="指定该订阅使用的下载器">
                <select v-model="editing.downloader_id" aria-label="下载器">
                  <option value="">默认</option>
                  <option
                    v-for="downloader in downloaderOptions"
                    :key="downloader.id"
                    :value="downloader.id"
                  >
                    {{ downloader.name || downloader.id }}
                  </option>
                </select>
              </SubscriptionField>
              <SubscriptionField
                label="保存路径"
                icon="folder"
                hint="指定该订阅的下载保存路径，留空自动使用设定的下载目录"
              >
                <input
                  v-model="editing.download_path"
                  aria-label="保存目录"
                  :list="editorID + '-directories'"
                  placeholder="自动"
                />
              </SubscriptionField>
              <datalist :id="editorID + '-directories'">
                <option v-for="path in downloadPaths" :key="path" :value="path" />
              </datalist>
            </div>
          </div>
          <div
            v-show="editingTab === 'advanced'"
            :id="editorID + '-advanced'"
            role="tabpanel"
            :aria-labelledby="editorID + '-advanced-tab'"
            class="subscription-advanced"
          >
            <div class="subscription-edit-grid">
              <SubscriptionField
                label="包含（关键字、正则式）"
                icon="plus"
                hint="包含规则，支持正则表达式"
              >
                <input
                  :value="subscriptionFilterValue(editing.policy, 'include')"
                  aria-label="包含"
                  @input="
                    editing.policy = setSubscriptionFilter(
                      editing.policy,
                      'include',
                      ($event.target as HTMLInputElement).value,
                    )
                  "
                />
              </SubscriptionField>
              <SubscriptionField
                label="排除（关键字、正则式）"
                icon="close"
                hint="排除规则，支持正则表达式"
              >
                <input
                  :value="subscriptionFilterValue(editing.policy, 'exclude')"
                  aria-label="排除"
                  @input="
                    editing.policy = setSubscriptionFilter(
                      editing.policy,
                      'exclude',
                      ($event.target as HTMLInputElement).value,
                    )
                  "
                />
              </SubscriptionField>
            </div>
            <div class="subscription-edit-grid">
              <SubscriptionField
                v-if="editing.media.media_type === 'tv'"
                label="指定季"
                icon="calendar"
                hint="指定订阅的季度"
              >
                <select
                  :value="editing.season"
                  aria-label="季"
                  @change="setSeason(Number(($event.target as HTMLSelectElement).value))"
                >
                  <option v-for="season in 99" :key="season" :value="season">
                    第 {{ season }} 季
                  </option>
                </select>
              </SubscriptionField>
              <SubscriptionField label="状态" icon="play" hint="暂停后停止自动检查，已下载文件保留">
                <select v-model="editing.state" aria-label="订阅状态">
                  <option value="active">启用</option>
                  <option value="paused">暂停</option>
                </select>
              </SubscriptionField>
            </div>
            <h3>资源筛选</h3>
            <div class="subscription-edit-grid">
              <SubscriptionField label="最低做种数" icon="upload">
                <input
                  v-model.number="editing.policy.min_seeders"
                  aria-label="最低做种数"
                  type="number"
                  min="0"
                />
              </SubscriptionField>
              <SubscriptionField label="编码" icon="film">
                <select v-model="editing.policy.codec" aria-label="编码">
                  <option value="">不限</option>
                  <option>HEVC</option>
                  <option>AVC</option>
                  <option>AV1</option>
                </select>
              </SubscriptionField>
              <SubscriptionField label="音轨语言" icon="globe" hint="多个语言用逗号分隔，如 zh, en">
                <input
                  :value="(editing.policy.languages || []).join(', ')"
                  aria-label="音轨语言"
                  @input="policyCSV('languages', $event)"
                />
              </SubscriptionField>
              <SubscriptionField label="字幕" icon="logs" hint="多个字幕用逗号分隔，如 zh-Hans, en">
                <input
                  :value="(editing.policy.subtitles || []).join(', ')"
                  aria-label="字幕"
                  @input="policyCSV('subtitles', $event)"
                />
              </SubscriptionField>
              <SubscriptionField label="最小体积（GiB）" icon="filter">
                <input
                  :value="editing.policy.min_bytes ? editing.policy.min_bytes / 1073741824 : ''"
                  aria-label="最小体积（GiB）"
                  type="number"
                  min="0"
                  step="0.1"
                  placeholder="不限制"
                  @input="policySize('min_bytes', $event)"
                />
              </SubscriptionField>
              <SubscriptionField label="最大体积（GiB）" icon="filter">
                <input
                  :value="editing.policy.max_bytes ? editing.policy.max_bytes / 1073741824 : ''"
                  aria-label="最大体积（GiB）"
                  type="number"
                  min="0"
                  step="0.1"
                  placeholder="不限制"
                  @input="policySize('max_bytes', $event)"
                />
              </SubscriptionField>
              <SubscriptionField
                label="优先发布组"
                icon="sort"
                hint="按优先顺序填写，多个发布组用逗号分隔"
              >
                <input
                  :value="(editing.policy.preferred_groups || []).join(', ')"
                  aria-label="优先发布组"
                  @input="policyCSV('preferred_groups', $event)"
                />
              </SubscriptionField>
              <SubscriptionField
                label="优先分辨率"
                icon="tv"
                hint="按优先顺序填写，如 2160p, 1080p"
              >
                <input
                  :value="(editing.policy.preferred_resolutions || []).join(', ')"
                  aria-label="优先分辨率"
                  @input="policyCSV('preferred_resolutions', $event)"
                />
              </SubscriptionField>
            </div>
            <label class="subscription-toggle">
              <input v-model="editing.policy.free_only" type="checkbox" />
              <span>仅免费资源</span>
            </label>
            <p class="hint">硬性要求用于筛选资源；优先条件只影响排序，不会自动升级已入库作品。</p>
            <VTextarea
              v-model="editing.custom_words"
              class="subscription-custom-words"
              label="自定义识别词"
              aria-label="自定义识别词"
              hint="只对该订阅使用的识别词"
              persistent-hint
              placeholder="屏蔽词&#10;被替换词 => 替换词&#10;前定位词 <> 后定位词 >> 集偏移量（EP）&#10;被替换词 => 替换词 && 前定位词 <> 后定位词 >> 集偏移量（EP）"
              prepend-inner-icon="mdi-text"
            />
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
        </template>
      </div>
      <footer v-if="editing.media" class="subscription-dialog-footer button-row">
        <button
          v-if="editing.id"
          class="cancel-subscription"
          :disabled="ui.busy > 0"
          @click="cancelSubscription"
        >
          取消订阅
        </button>
        <button
          v-if="
            !editing.id && editing.media.media_type === 'tv' && editing.media.seasons?.length > 1
          "
          @click="openMulti"
        >
          选择多个季度订阅
        </button>
        <span class="subscription-action-spacer"></span>
        <button class="primary subscription-save" :disabled="ui.busy > 0" @click="save">
          <AppIcon name="save" />保存
        </button>
      </footer>
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
.subscription-actions {
  position: fixed;
  right: 24px;
  bottom: 24px;
  z-index: 20;
}
.subscription-add {
  width: 56px;
  min-width: 56px;
  height: 56px;
  min-height: 56px;
  padding: 0;
  border-radius: 50%;
}
.subscription-add :deep(.app-icon) {
  width: 28px;
  height: 28px;
}
.toolbar {
  gap: 12px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}
.toolbar input {
  max-width: 230px;
}
.toolbar > select {
  min-width: 115px;
}
.subscription-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(240px, 100%), 1fr));
  gap: 16px;
  align-items: start;
}
.subscription-card {
  position: relative;
  isolation: isolate;
  display: block;
  min-width: 0;
  min-height: 160px;
  aspect-ratio: 3 / 2;
  padding: 0;
  border: 0;
  border-radius: 8px;
  background: #1f2937;
  box-shadow: none;
  transition: transform 0.3s ease;
}
.subscription-card:has(.subscription-card-content:hover) {
  transform: translateY(-4px);
}
.subscription-card.paused {
  opacity: 0.65;
}
.subscription-card > .subscription-backdrop {
  position: absolute;
  z-index: -2;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: top;
  border-radius: inherit;
  box-shadow: none;
}
.subscription-card::before {
  position: absolute;
  z-index: -1;
  inset: 0;
  content: '';
  border-radius: inherit;
  background: linear-gradient(180deg, rgba(31, 41, 55, 0.47), #1f2937);
}
.subscription-card-content {
  position: relative;
  display: flex;
  flex-direction: column;
  justify-content: flex-start;
  width: 100%;
  height: 100%;
  min-height: inherit;
  padding: 12px 20px 28px;
  border: 0;
  border-radius: inherit;
  background: transparent;
  color: white;
  text-align: left;
  box-shadow: none;
}
.subscription-card-content:hover {
  background: transparent;
  box-shadow: none;
}
.subscription-heading {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding-right: 16px;
}
.subscription-poster {
  flex: 0 0 48px;
  width: 48px;
  height: 72px;
  border-radius: 6px;
  object-fit: cover;
}
.subscription-name {
  min-width: 0;
}
.subscription-name > span {
  color: white;
  font-size: 14px;
  font-weight: 500;
}
.subscription-card h3 {
  display: -webkit-box;
  overflow: hidden;
  margin: 4px 0 0;
  color: white;
  font-size: 18px;
  font-weight: 700;
  line-height: 1.5;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}
.subscription-progress-text {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: auto;
  padding-top: 16px;
  color: white;
  font-size: 14px;
  line-height: 22px;
}
.subscription-progress-text :deep(.app-icon) {
  width: 20px;
  height: 20px;
}
.subscription-state {
  position: absolute;
  right: 8px;
  bottom: 10px;
  color: #d1d5db;
  font-size: 12px;
  font-weight: 400;
}
.subscription-progress {
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  width: 100%;
  height: 4px;
  margin: 0;
  border: 0;
  border-radius: 0 0 8px 8px;
  overflow: hidden;
  accent-color: var(--success, #56ca00);
}
.subscription-progress::-webkit-progress-bar {
  background: rgba(86, 202, 0, 0.25);
}
.subscription-progress::-webkit-progress-value {
  background: var(--success, #56ca00);
}
.subscription-progress::-moz-progress-bar {
  background: var(--success, #56ca00);
}
.subscription-menu {
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
  position: absolute;
  top: 4px;
  right: 4px;
  left: 4px;
  z-index: 2;
  pointer-events: none;
}
.subscription-card:has(.subscription-menu[open]) {
  z-index: 10;
}
.subscription-menu > summary {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  border-radius: 50%;
  color: white;
  cursor: pointer;
  list-style: none;
  margin-left: auto;
  pointer-events: auto;
}
.subscription-menu[open] > summary {
  margin-bottom: 0;
}
.subscription-menu > summary::-webkit-details-marker {
  display: none;
}
.subscription-menu > summary:hover {
  background: rgba(255, 255, 255, 0.1);
}
.subscription-menu-panel {
  position: absolute;
  top: 100%;
  right: 0;
  width: min(288px, 100%);
  max-width: calc(100vw - 32px);
  max-height: min(560px, calc(100dvh - 140px));
  overflow: auto;
  pointer-events: auto;
  padding: 8px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
  color: var(--text);
  text-align: left;
}
.subscription-menu-actions {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.subscription-menu-actions > button {
  justify-content: flex-start;
  gap: 12px;
  width: 100%;
  min-height: 40px;
  padding: 8px 12px;
  border: 0;
  background: transparent;
  color: var(--text);
  font-size: 16px;
  box-shadow: none;
}
.subscription-menu-actions > button:hover {
  background: var(--accent-soft);
}
.subscription-menu-actions > button.danger {
  color: var(--danger);
}
.subscription-metadata {
  margin-top: 8px;
  padding: 8px 12px 0;
  border-top: 1px solid var(--line);
  font-size: 12px;
  line-height: 1.6;
  color: var(--muted);
}
.subscription-metadata p {
  margin: 0 0 8px;
}
.subscription-metadata dl {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 5px 12px;
  margin: 8px 0;
}
.subscription-metadata dl > div {
  display: flex;
  justify-content: space-between;
  gap: 5px;
}
.subscription-metadata dd {
  margin: 0;
  color: var(--text);
}
.calendar-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin: 20px;
}
.calendar-toolbar h2 {
  margin: 0;
  color: var(--text);
  font-size: 20px;
  font-weight: 500;
  white-space: nowrap;
}
.calendar-toolbar .button-row {
  gap: 0;
}
.calendar-toolbar .button-row > button {
  min-width: 38px;
  padding: 4px 8px;
  border: 0;
  background: transparent;
  color: var(--text);
  box-shadow: none;
}
.calendar-toolbar .button-row > button:first-child,
.calendar-toolbar .button-row > button:last-child {
  font-size: 24px;
}
.calendar-toolbar .button-row > button:hover {
  color: var(--accent);
}
.calendar-toolbar .segmented {
  display: flex;
  gap: 0;
  padding: 0;
  border: 1px solid color-mix(in srgb, var(--accent), transparent 50%);
  border-radius: 6px;
  overflow: hidden;
  background: transparent;
}
.calendar-toolbar .segmented > button {
  min-height: 36px;
  padding: 4px 16px;
  border: 0;
  border-radius: 0;
  background: transparent;
  color: var(--accent);
  font-size: 14px;
  font-weight: 500;
  box-shadow: none;
}
.calendar-toolbar .segmented > button + button {
  border-left: 1px solid color-mix(in srgb, var(--accent), transparent 50%);
}
.calendar-toolbar .segmented > button.selected {
  background: var(--accent-soft);
}
.calendar-grid {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  border: 0;
  border-top: 1px solid var(--line);
  border-left: 1px solid var(--line);
  border-radius: 0;
  background: transparent;
  box-shadow: none;
  overflow: visible;
}
.calendar-weekday {
  padding: 8px;
  border-color: var(--line);
  background: transparent;
  color: var(--text);
  font-size: 14px;
  font-weight: 500;
}
.calendar-day,
.calendar-grid.week-view .calendar-day {
  min-height: 130px;
  padding: 5px;
  border-color: var(--line);
  background: transparent;
  overflow: hidden;
}
.calendar-day > strong,
.calendar-day.today > strong {
  display: block;
  width: auto;
  height: auto;
  margin: 0 0 5px;
  padding: 0;
  border-radius: 0;
  background: transparent;
  color: var(--text);
  text-align: right;
  font-size: 14px;
  font-weight: 400;
  line-height: 24px;
}
.calendar-day.today {
  background-image: linear-gradient(to bottom, #af85fd, rgba(58, 53, 65, 0.04));
}
.calendar-day.outside > strong {
  opacity: 0.3;
}
.event-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  justify-content: flex-start;
  gap: 8.8px;
  width: 100%;
  margin-bottom: 4.8px;
  padding: 6.4px;
  border: 0;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.72);
  color: var(--text);
  text-align: left;
  box-shadow: none;
}
.event-card:hover,
.event-card.complete {
  background: rgba(255, 255, 255, 0.85);
  border: 0;
  box-shadow: none;
}
.event-card > img {
  width: 56px;
  height: 84px;
  flex: 0 0 56px;
  border-radius: 6px;
  object-fit: cover;
}
.event-card > .event-info {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 3.2px;
  min-width: 0;
}
.event-card .event-info > b {
  display: -webkit-box;
  overflow: hidden;
  color: var(--text);
  font-size: 14px;
  font-weight: 700;
  line-height: 1.28;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow-wrap: anywhere;
}
.event-card .event-info > small {
  color: var(--muted);
  font-size: 11.5px;
  font-weight: 500;
  line-height: 1.25;
}
.event-card.complete .event-info > small:last-child {
  color: var(--success, #56ca00);
}
.calendar-episode-overlay {
  display: none;
}
.calendar-library-check {
  position: absolute;
  top: 9px;
  left: 43px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: 2px solid white;
  border-radius: 50%;
  background: var(--success, #56ca00);
  color: white;
}
.calendar-library-check :deep(.app-icon) {
  width: 11px;
  height: 11px;
}
.calendar-agenda {
  padding: 0 20px;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}
.calendar-agenda section {
  padding: 16px 0;
  border-bottom: 1px solid var(--line);
}
.calendar-agenda h3 {
  margin: 0 0 10px;
  color: var(--text);
  font-size: 14px;
  font-weight: 500;
}
.agenda-entry {
  display: flex;
  align-items: center;
  gap: 16px;
  width: 100%;
  padding: 10px;
  border: 0;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.72);
  text-align: left;
  box-shadow: none;
}
.agenda-entry + .agenda-entry {
  margin-top: 8px;
}
.agenda-entry:hover {
  background: var(--accent-soft);
}
.agenda-entry > img {
  width: 36px;
  height: 54px;
  border-radius: 6px;
  object-fit: cover;
}
.agenda-entry > strong {
  flex: 1;
  font-size: 14px;
  color: var(--text);
}
.agenda-entry > span {
  font-size: 12px;
  color: var(--muted);
}
.subscription-dialog {
  display: flex;
  flex-direction: column;
  width: min(720px, calc(100vw - 48px));
  max-width: 720px;
  max-height: min(92dvh, 1024px);
  padding: 0;
  border-radius: 8px;
  overflow: hidden;
}
.subscription-dialog-header {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 20px;
}
.subscription-dialog-title {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.subscription-dialog-title > :deep(.app-icon) {
  width: 24px;
  height: 24px;
  color: var(--muted);
}
.subscription-dialog-title > div {
  min-width: 0;
}
.subscription-dialog-title h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 500;
}
.subscription-dialog-title p {
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-dialog-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0 20px 20px;
}
.subscription-edit-tabs {
  display: flex;
  width: 100%;
  margin: 0 0 28px;
  border-bottom: 1px solid var(--line);
}
.subscription-edit-tabs > button {
  flex: 0 0 90px;
  min-height: 48px;
  padding: 12px 16px;
  font-size: 16px;
  font-weight: 500;
}
.subscription-basic {
  display: grid;
  gap: 24px;
}
.subscription-edit-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: start;
  gap: 24px;
}
.subscription-advanced > .subscription-edit-grid + .subscription-edit-grid {
  margin-top: 24px;
}
.subscription-custom-words {
  margin-top: 24px;
}
.subscription-custom-words :deep(.v-field textarea) {
  border: 0;
  border-radius: 0;
  background: transparent;
  padding: var(--v-field-input-padding-top) var(--v-field-padding-end)
    var(--v-field-input-padding-bottom) var(--v-field-padding-start);
  box-shadow: none;
  color: inherit;
  font: inherit;
  line-height: 1.5;
}
.subscription-custom-words :deep(.v-label) {
  flex-direction: row;
  gap: 0;
}
.subscription-dialog-footer {
  display: flex;
  flex: none;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0;
  padding: 12px 24px 16px;
}
.subscription-action-spacer {
  flex: 1;
}
.subscription-dialog-footer .cancel-subscription {
  border-color: transparent;
  background: rgba(255, 76, 81, 0.12);
  color: var(--danger);
  box-shadow: none;
}
.subscription-save {
  gap: 8px;
  padding-inline: 20px;
}
.three-columns {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
.subscription-dialog-body :deep(.form-grid) {
  gap: 24px;
  margin-top: 20px;
  margin-bottom: 20px;
}
.subscription-sites {
  position: relative;
  min-width: 0;
}
.subscription-sites-label {
  position: absolute;
  z-index: 1;
  top: -8px;
  left: 12px;
  padding: 0 4px;
  background: var(--surface);
  color: var(--muted);
  font-size: 12px;
  line-height: 16px;
}
.subscription-sites:focus-within .subscription-sites-label {
  color: var(--accent);
}
.site-selection {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 56px;
  padding: 7px 12px;
  border: 1px solid rgba(58, 53, 65, 0.22);
  border-radius: 6px;
}
.site-selection:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}
.site-selection > :deep(.app-icon) {
  width: 24px;
  height: 24px;
  color: var(--muted);
}
.site-values {
  display: flex;
  flex: 1;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.site-query {
  flex: 1;
  width: 80px;
  min-width: 60px;
  min-height: 32px;
  padding: 0;
  border: 0;
  background: transparent;
  box-shadow: none;
  font-size: 16px;
}
.site-query:focus {
  box-shadow: none;
}
.subscription-sites > small {
  display: block;
  margin: 4px 12px 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
}
.site-chip {
  display: inline-flex;
  align-items: center;
  height: 24px;
  padding: 0 10px;
  border-radius: 12px;
  background: rgba(58, 53, 65, 0.08);
  color: var(--text);
  font-size: 12px;
  line-height: 24px;
}
.site-dropdown {
  flex: none;
  min-width: 24px;
  min-height: 32px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--muted);
  text-align: left;
  box-shadow: none;
}
.site-options {
  position: absolute;
  z-index: 10;
  top: 60px;
  left: 0;
  width: 100%;
  max-height: 210px;
  overflow: auto;
  padding: 8px;
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--surface);
  box-shadow: 0 4px 12px rgba(58, 53, 65, 0.12);
}
.site-options > button {
  display: flex;
  justify-content: flex-start;
  gap: 12px;
  width: 100%;
  min-height: 40px;
  padding: 8px 12px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  box-shadow: none;
  text-align: left;
}
.site-options > button:hover,
.site-options > .site-option-active {
  background: var(--accent-soft);
}
.site-option-check {
  display: inline-flex;
  width: 18px;
  height: 18px;
  border: 1.5px solid var(--muted);
  border-radius: 3px;
}
.site-option-check.checked {
  border-color: var(--accent);
  background: var(--accent);
  color: white;
}
.site-option-check :deep(.app-icon) {
  width: 15px;
  height: 15px;
}
.subscription-advanced > h3 {
  margin-top: 24px;
  padding-top: 20px;
  border-top: 1px solid var(--line);
  color: var(--text);
  font-size: 16px;
  font-weight: 500;
}
.subscription-advanced > :deep(.section-heading) {
  margin-top: 24px;
}
.subscription-toggle {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 12px;
  margin-top: 20px;
}
.subscription-toggle input {
  position: relative;
  width: 34px;
  height: 20px;
  padding: 0;
  border: 0;
  border-radius: 12px;
  appearance: none;
  background: rgba(58, 53, 65, 0.28);
}
.subscription-toggle input::after {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: white;
  content: '';
}
.subscription-toggle input:checked {
  background: var(--accent);
}
.subscription-toggle input:checked::after {
  transform: translateX(14px);
}
.event-detail {
  display: flex;
  align-items: flex-start;
  gap: 20px;
  margin: 16px 0;
}
.event-detail > img {
  width: 100px;
  border-radius: 8px;
  box-shadow: none;
}
.event-detail h3 {
  margin-top: 0;
}
.modal footer {
  margin-top: 20px;
  justify-content: flex-end;
}
.subscription-dialog > .subscription-dialog-footer {
  margin-top: 0;
}
@media (max-width: 1279px) {
  .event-card,
  .event-card:hover,
  .event-card.complete {
    display: block;
    width: clamp(40px, 5.8vw, 64px);
    min-height: 0;
    padding: 0;
    background: transparent;
    border-radius: 6px;
    overflow: hidden;
  }
  .event-card > img {
    display: block;
    width: 100%;
    height: auto;
    aspect-ratio: 2 / 3;
  }
  .event-card > .event-info {
    display: none;
  }
  .calendar-entry .calendar-episode-overlay {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    display: block;
    padding: 2px;
    background: rgba(0, 0, 0, 0.58);
    color: white;
    font-size: 10px;
    text-align: center;
    line-height: 1.4;
  }
  .calendar-library-check {
    top: 2px;
    right: 2px;
    left: auto;
    width: 16px;
    height: 16px;
  }
}
@media (max-width: 959px) {
  .subscription-editor-backdrop {
    padding: 0;
    overflow: hidden;
  }
  .subscription-dialog {
    width: 100vw;
    max-width: 100vw;
    height: 100dvh;
    max-height: 100dvh;
    border-radius: 0;
  }
  .subscription-edit-grid,
  .three-columns {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 600px) {
  .subscription-actions {
    right: 16px;
    bottom: 16px;
  }
  .subscription-grid {
    gap: 12px;
  }
  .subscription-card {
    min-height: 150px;
    aspect-ratio: auto;
  }
  .subscription-card-content {
    padding: 12px 10px 28px;
  }
  .subscription-heading {
    gap: 8px;
    padding-right: 0;
  }

  .subscription-name > span {
    font-size: 12px;
  }
  .subscription-card h3 {
    margin-top: 8px;
    font-size: 16px;
  }
  .subscription-progress-text {
    gap: 4px;
    font-size: 12px;
  }
  .subscription-menu-panel {
    width: min(288px, 100%);
  }
  .calendar-toolbar {
    gap: 8px;
    margin: 12px 0;
    flex-wrap: wrap;
  }
  .calendar-toolbar h2 {
    order: -1;
    width: 100%;
    text-align: center;
  }
  .calendar-toolbar .segmented > button {
    padding-inline: 12px;
  }
  .calendar-weekday {
    padding-inline: 2px;
    font-size: 12px;
  }
  .calendar-day,
  .calendar-grid.week-view .calendar-day {
    min-height: 100px;
    padding: 3px;
  }
  .calendar-day > strong {
    font-size: 12px;
  }
  .event-card {
    max-width: 100%;
  }
  .agenda-entry {
    gap: 8px;
    flex-wrap: wrap;
  }
  .subscription-dialog-header {
    padding: 12px 16px;
  }
  .subscription-dialog-body {
    padding-inline: 16px;
  }
  .subscription-dialog-footer {
    padding-inline: 16px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .subscription-card {
    transition: none;
  }
}
</style>

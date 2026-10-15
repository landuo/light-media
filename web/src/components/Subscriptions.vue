<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
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
import { subscriptionTarget, subscriptionTotal } from '../features/subscriptions';
import Policy from './Policy.vue';
import Schedule from './Schedule.vue';
import MappingRules from './MappingRules.vue';
import MultiSeasonSubscribe from './MultiSeasonSubscribe.vue';
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
  calendarMonth = ref(new Date().toISOString().slice(0, 7)),
  loading = ref(false);
const calendarView = ref('month'),
  weekAnchor = ref(new Date().toLocaleDateString('en-CA'));
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
const isCalendar = computed(() => props.view === 'calendar');
function localDay(d: Date) {
  return (
    d.getFullYear() +
    '-' +
    String(d.getMonth() + 1).padStart(2, '0') +
    '-' +
    String(d.getDate()).padStart(2, '0')
  );
}
function weekStart() {
  const d = new Date(weekAnchor.value + 'T00:00:00');
  d.setDate(d.getDate() - d.getDay());
  return d;
}
const days = computed(() => {
  if (calendarView.value === 'week') {
    const start = weekStart();
    if (Number.isNaN(start.valueOf())) return [];
    return Array.from({ length: 7 }, (_, i) => {
      const d = new Date(start);
      d.setDate(d.getDate() + i);
      return localDay(d);
    });
  }
  if (!/^\d{4}-\d{2}$/.test(calendarMonth.value)) return [];
  const [y, m] = calendarMonth.value.split('-').map(Number),
    count = new Date(y!, m!, 0).getDate(),
    offset = new Date(y!, m! - 1, 1).getDay();
  return [
    ...Array(offset).fill(null),
    ...Array.from(
      { length: count },
      (_, i) => `${calendarMonth.value}-${String(i + 1).padStart(2, '0')}`,
    ),
  ];
});
async function load() {
  loading.value = true;
  try {
    const response = await api('/subscriptions');
    subs.value = Array.isArray(response) ? response : response?.items || [];
    if (isCalendar.value) {
      const visible = days.value.filter(Boolean);
      if (!visible.length) throw new Error('请选择有效的日历日期');
      const r = await api('/calendar?from=' + visible[0] + '&to=' + visible[visible.length - 1]);
      entries.value = r.entries || [];
    }
  } catch (e) {
    await action(() => Promise.reject(e));
  } finally {
    loading.value = false;
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
        excluded: [],
        episode_dates: {},
        allow_early: [],
        policy: emptyPolicy(),
        schedule: emptySchedule(),
        mappings: [],
        downloader_id: ui.settings?.default_downloader || ui.settings?.downloaders?.[0]?.id || '',
        root_id: ui.settings?.default_root || ui.settings?.roots?.[0]?.id || '',
        state: 'active',
      };
  multiSeason.value = false;
  convertLegacy.value = false;
  editing.value.start_episode ||= 1;
  excluded.value = (editing.value.excluded || []).join(',');
  allowEarly.value = (editing.value.allow_early || []).join(',');
  dates.value = Object.entries(editing.value.episode_dates || {})
    .map(([ep, d]) => `${ep}=${d}`)
    .join('\n');
  editing.value.mappings ||= [];
  for (const m of editing.value.mappings) m.source ||= { site: '', group: '', form: '' };
}
async function find() {
  lookupResults.value =
    (await action(() => api('/catalog/search?q=' + encodeURIComponent(lookup.value) + '&page=1')))
      ?.results || [];
}
function availableRoots(kind: string) {
  return (ui.settings?.roots || []).filter(
    (r: any) => !r.kind || r.kind === kind || r.kind === 'mixed',
  );
}
async function pick(m: any) {
  editing.value.media = await action(() =>
    api('/catalog/detail?kind=' + m.media_type + '&id=' + m.id),
  );
  editing.value.season =
    editing.value.media?.media_type === 'movie'
      ? 0
      : editing.value.media?.seasons?.find((s: any) => s.season_number > 0)?.season_number || 1;
  editing.value.target_scope = editing.value.media?.media_type === 'movie' ? 'movie' : 'season';
  const roots = availableRoots(editing.value.media?.media_type);
  if (!roots.some((r: any) => r.id === editing.value.root_id))
    editing.value.root_id = roots[0]?.id || '';
  lookupResults.value = [];
}
function formInput() {
  const s = clone(editing.value);
  if (!s.media) throw new Error('请先选择影视作品');
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
  if (calendarView.value === 'week') {
    const d = weekStart();
    d.setDate(d.getDate() + offset * 7);
    weekAnchor.value = localDay(d);
  } else {
    const [y, m] = calendarMonth.value.split('-').map(Number);
    const d = new Date(y!, m! - 1 + offset, 1);
    calendarMonth.value = d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0');
  }
  load();
}
onMounted(async () => {
  await load();
  if (props.view.includes('/new/')) {
    begin();
    const [path, qs] = props.view.split('?'),
      p = path!.split('/'),
      q = new URLSearchParams(qs);
    await pick({ media_type: p[2], id: +p[3]! });
    editing.value.season =
      editing.value.media?.media_type === 'movie' ? 0 : +(q.get('season') || editing.value.season);
    editing.value.start_episode = Math.max(1, +(q.get('start_episode') || 1));
  }
});
</script>
<template>
  <div class="page-heading">
    <div>
      <p class="eyebrow">{{ isCalendar ? 'RELEASE CALENDAR' : 'SUBSCRIPTIONS' }}</p>
      <h1>{{ isCalendar ? '追剧日历' : '我的订阅' }}</h1>
      <p class="muted">
        {{
          isCalendar
            ? '更新时间与资源检查各有节奏，窗口外也会保留兜底检查。'
            : '每季独立订阅，从指定集数开始自动追更、下载和入库。'
        }}
      </p>
    </div>
    <button class="primary" @click="begin()">＋ 新建订阅</button>
  </div>
  <template v-if="isCalendar"
    ><div class="toolbar">
      <div class="segmented">
        <button
          :class="{ selected: calendarView === 'month' }"
          @click="
            calendarView = 'month';
            load();
          "
        >
          月视图</button
        ><button
          :class="{ selected: calendarView === 'week' }"
          @click="
            calendarView = 'week';
            load();
          "
        >
          周视图
        </button>
      </div>
      <button @click="month(-1)">‹</button
      ><input
        v-if="calendarView === 'month'"
        type="month"
        v-model="calendarMonth"
        @change="load"
      /><input v-else type="date" v-model="weekAnchor" @change="load" /><button @click="month(1)">
        ›</button
      ><span class="hint push-right">{{ entries.length }} 条更新安排</span>
    </div>
    <div :class="['calendar-grid', { 'week-view': calendarView === 'week' }]">
      <div
        v-for="day in ['日', '一', '二', '三', '四', '五', '六']"
        :key="day"
        class="calendar-weekday"
      >
        周{{ day }}
      </div>
      <div
        v-for="(day, i) in days"
        :key="i"
        :class="[
          'calendar-day',
          { blank: !day, today: day === new Date().toLocaleDateString('en-CA') },
        ]"
      >
        <strong v-if="day">{{ Number(day.slice(-2)) }}</strong
        ><button
          v-for="e in entries.filter((x) => x.date === day)"
          :key="e.subscription_id + ':' + e.episode"
          class="calendar-entry"
          @click="begin(subs.find((s) => s.id === e.subscription_id))"
        >
          <span>{{ e.time || '时间待定' }}</span
          ><b>{{ e.title }}</b
          ><small
            >S{{ e.season }} · {{ e.confirmed && e.episode ? 'E' + e.episode : '集号待确认' }} ·
            {{ label(e.state || e.status) }}</small
          ><small>{{
            ({ metadata: '元数据日期', manual_episode: '人工日期', prediction: '周期预测' } as any)[
              e.source
            ] || e.source
          }}</small>
        </button>
      </div>
    </div></template
  >
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
            <span class="badge">{{ label(s.state) }}</span>
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
      <template v-else
        ><div class="notice">
          <strong>{{ editing.media.title }}</strong> · {{ editing.media.year }} · TMDB
          {{ editing.media.id }}
        </div>
        <div class="form-grid">
          <label v-if="editing.media.media_type === 'tv'"
            >订阅季<select v-model.number="editing.season">
              <option
                v-for="ss in (editing.media.seasons || []).filter((s: any) => s.season_number > 0)"
                :key="ss.season_number"
                :value="ss.season_number"
              >
                {{ ss.name || '第 ' + ss.season_number + ' 季' }} · {{ ss.episode_count }} 集
              </option>
              <option v-if="!editing.media.seasons?.length" :value="editing.season">
                第 {{ editing.season }} 季
              </option>
            </select></label
          ><label v-if="editing.media.media_type === 'tv'"
            >开始集数<input
              v-model.number="editing.start_episode"
              type="number"
              min="1"
              step="1"
            /><small class="hint">包含这一集，自动追踪本季后续更新。</small></label
          ><label
            >下载器<select v-model="editing.downloader_id">
              <option v-for="d in ui.settings?.downloaders || []" :key="d.id" :value="d.id">
                {{ d.name || d.id }}
              </option>
            </select></label
          ><label
            >入库目录<select v-model="editing.root_id">
              <option
                v-for="r in availableRoots(editing.media.media_type)"
                :key="r.id"
                :value="r.id"
              >
                {{ r.name || r.path }}
              </option>
            </select></label
          ><label
            >状态<select v-model="editing.state">
              <option value="active">启用</option>
              <option value="paused">暂停</option>
            </select></label
          >
        </div>
        <div
          v-if="editing.id && editing.target_scope !== 'season' && editing.episodes?.length"
          class="notice"
        >
          此历史订阅只跟踪 {{ editing.episodes.join('、') }} 集。<label class="check"
            ><input v-model="convertLegacy" type="checkbox" />改为从开始集数起整季追更</label
          >
        </div>
        <p
          v-if="
            editing.media.media_type === 'tv' &&
            editing.media.seasons?.some((s: any) => s.season_number === 0)
          "
          class="hint"
        >
          特别篇（第 0 季）暂不支持自动订阅。
        </p>
        <details>
          <summary>筛选规则</summary>
          <Policy :model="editing.policy" />
        </details>
        <details>
          <summary>更新时间与检查周期</summary>
          <Schedule :model="editing.schedule" />
        </details>
        <details>
          <summary>单集边界与映射</summary>
          <div class="form-grid">
            <label>排除集数<input v-model="excluded" placeholder="这些集数不再补齐" /></label
            ><label
              >允许提前下载的集数<input v-model="allowEarly" placeholder="仅对明确列出的集数生效"
            /></label>
          </div>
          <label
            >单集日期覆盖（每行：集数=YYYY-MM-DD）<textarea
              v-model="dates"
              rows="3"
              placeholder="17=2026-10-16"
            ></textarea></label
          ><MappingRules
            :model="editing.mappings"
            :media-id="editing.media.media_type + ':' + editing.media.id"
            :season="editing.season"
          />
          <p class="hint">只有明确确认的映射生效。未知绝对集号将等待人工核对。</p>
        </details>
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
          ><button
            class="primary"
            :disabled="ui.busy > 0 || !editing.downloader_id || !editing.root_id"
            @click="save"
          >
            保存订阅
          </button>
        </div></template
      >
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
</style>

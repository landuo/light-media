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
import Policy from './Policy.vue';
import Schedule from './Schedule.vue';
import MappingRules from './MappingRules.vue';
const props = defineProps<{ view: string }>(),
  subs = ref<any[]>([]),
  entries = ref<any[]>([]),
  editing = ref<any>(null),
  episodes = ref(''),
  excluded = ref(''),
  allowEarly = ref(''),
  dates = ref(''),
  mappingJSON = ref('[]'),
  lookup = ref(''),
  lookupKind = ref('tv'),
  lookupResults = ref<any[]>([]),
  calendarMonth = ref(new Date().toISOString().slice(0, 7)),
  loading = ref(false);
const calendarView = ref('month'),
  weekAnchor = ref(new Date().toLocaleDateString('en-CA'));
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
  episodes.value = (editing.value.episodes || []).join(',');
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
    (
      await action(() =>
        api(
          '/catalog/search?q=' +
            encodeURIComponent(lookup.value) +
            '&kind=' +
            lookupKind.value +
            '&page=1',
        ),
      )
    )?.results || [];
}
async function pick(m: any) {
  editing.value.media = await action(() =>
    api('/catalog/detail?kind=' + m.media_type + '&id=' + m.id),
  );
  editing.value.season =
    editing.value.media?.media_type === 'movie'
      ? 0
      : editing.value.media?.seasons?.find((s: any) => s.season_number > 0)?.season_number || 1;
  lookupResults.value = [];
}
async function save() {
  const r = await action(async () => {
    const s = clone(editing.value);
    if (!s.media) throw new Error('请先选择影视作品');
    s.episodes = episodeNumbers(episodes.value);
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
    episodes.value = q.get('episodes') || '';
  }
});
</script>
<template>
  <div class="page-heading">
    <div>
      <p class="eyebrow">{{ isCalendar ? 'RELEASE CALENDAR' : 'SUBSCRIPTIONS' }}</p>
      <h1>{{ isCalendar ? '追剧日历' : '让更新如期而至' }}</h1>
      <p class="muted">
        {{
          isCalendar
            ? '更新时间与资源检查各有节奏，窗口外也会保留兜底检查。'
            : '按作品与标准季集订阅，规则、周期和缺集状态都由你掌控。'
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
    <div class="subscription-grid">
      <article v-for="s in subs" :key="s.id" class="subscription-card">
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
            {{ s.episodes?.length ? s.episodes.length + ' 集' : '全部集数' }}
          </p>
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
  <div v-if="editing" class="modal-backdrop">
    <section class="modal wide">
      <div class="section-heading">
        <h2>{{ editing.id ? '编辑订阅' : '新建订阅' }}</h2>
        <button class="icon-button" @click="editing = null" aria-label="关闭">×</button>
      </div>
      <div v-if="!editing.media">
        <form class="inline-form" @submit.prevent="find">
          <select v-model="lookupKind">
            <option value="tv">剧集</option>
            <option value="movie">电影</option></select
          ><input v-model="lookup" placeholder="搜索要订阅的作品" required /><button
            class="primary"
          >
            搜索
          </button>
        </form>
        <div class="lookup-list">
          <button v-for="m in lookupResults" :key="m.id" @click="pick(m)">
            {{ m.title }} <span>{{ m.year }} · TMDB {{ m.id }}</span>
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
            >季<input v-model.number="editing.season" type="number" min="0" /></label
          ><label v-if="editing.media.media_type === 'tv'"
            >目标集数<input v-model="episodes" placeholder="留空为整季，或 1,2,5-8" /></label
          ><label
            >下载器<select v-model="editing.downloader_id">
              <option v-for="d in ui.settings?.downloaders || []" :key="d.id" :value="d.id">
                {{ d.name || d.id }}
              </option>
            </select></label
          ><label
            >入库目录<select v-model="editing.root_id">
              <option v-for="r in ui.settings?.roots || []" :key="r.id" :value="r.id">
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
        <details open>
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
</template>

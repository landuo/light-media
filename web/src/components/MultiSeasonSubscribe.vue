<script setup lang="ts">
import { ref, computed } from 'vue';
import { api, clone, errorText, image, toast } from '../api';
import { submitSeasonBatch, type SeasonSubscriptionRow } from '../features/subscriptions';
const props = defineProps<{ template: any; existing: any[] }>();
const emit = defineEmits<{ close: [created: boolean]; changed: [] }>();
const rows = ref<SeasonSubscriptionRow[]>(
  (props.template.media.seasons || [])
    .filter((s: any) => s.season_number > 0)
    .map((s: any) => ({
      season: s.season_number,
      title: s.name || '第 ' + s.season_number + ' 季',
      episode_count: s.episode_count,
      poster_path: s.poster_path,
      selected: s.season_number === props.template.season,
      start_episode: 1,
      state: props.existing.some(
        (v) =>
          v.media?.id === props.template.media.id &&
          v.media?.media_type === 'tv' &&
          v.season === s.season_number &&
          v.state !== 'cancelled',
      )
        ? 'conflict'
        : 'ready',
      message: props.existing.some(
        (v) =>
          v.media?.id === props.template.media.id &&
          v.media?.media_type === 'tv' &&
          v.season === s.season_number &&
          v.state !== 'cancelled',
      )
        ? '本季已有订阅'
        : '',
    })),
);
const running = ref(false),
  checked = computed(() => rows.value.filter((s) => s.selected)),
  completed = computed(() => rows.value.filter((s) => s.state === 'success').length);
function all() {
  const available = rows.value.filter((s) => s.state !== 'success' && s.state !== 'conflict');
  const selected = !available.every((s) => s.selected);
  for (const s of available) s.selected = selected;
}
async function submit() {
  if (running.value) return;
  running.value = true;
  const base = clone(props.template);
  delete base.id;
  Object.assign(base, {
    media_type: base.media.media_type,
    tmdb_id: base.media.id,
    target_scope: 'season',
    episodes: [],
  });
  await submitSeasonBatch(
    rows.value,
    base,
    async (body) => {
      const r = await api('/subscriptions', body);
      emit('changed');
      return r;
    },
    errorText,
  );
  running.value = false;
  const failed = rows.value.filter((s) => s.selected && s.state === 'failed').length,
    conflicts = rows.value.filter((s) => s.selected && s.state === 'conflict').length;
  toast(
    `多季订阅：成功 ${completed.value} 季${conflicts ? '，已存在 / 冲突 ' + conflicts + ' 季' : ''}${failed ? '，失败 ' + failed + ' 季' : ''}`,
    failed > 0,
  );
}
</script>
<template>
  <div class="modal-backdrop">
    <section class="modal wide" role="dialog" aria-modal="true" aria-label="订阅多个季度">
      <div class="section-heading">
        <h2>订阅多个季度</h2>
        <button
          class="icon-button"
          :disabled="running"
          @click="emit('close', completed > 0)"
          aria-label="关闭"
        >
          ×
        </button>
      </div>
      <h3>{{ template.media.title }}</h3>
      <p class="muted">选择要追更的季，每季独立建立订阅。可分别设置开始集数。</p>
      <p class="hint">
        下载器、入库目录、筛选与更新规则沿用当前订阅表单。特别篇（第 0 季）暂不支持自动订阅。
      </p>
      <div class="button-row">
        <button class="small" :disabled="running" @click="all">选择 / 取消全部可订阅季</button
        ><span class="hint">已选 {{ checked.length }} 季 · 已成功 {{ completed }} 季</span>
      </div>
      <div class="season-list">
        <div v-for="s in rows" :key="s.season" class="season-row">
          <input
            v-model="s.selected"
            :aria-label="'订阅' + s.title"
            type="checkbox"
            :disabled="running || s.state === 'success' || s.state === 'conflict'"
          /><img
            v-if="image(s.poster_path || template.media.poster_path, 'w92')"
            :src="image(s.poster_path || template.media.poster_path, 'w92')"
            :alt="s.title"
          />
          <div class="season-info">
            <strong>{{ s.title }}</strong
            ><small>{{ s.episode_count || '集数待定' }}{{ s.episode_count ? ' 集' : '' }}</small
            ><small :class="{ green: s.state === 'success', 'error-text': s.state === 'failed' }"
              >{{
                (
                  {
                    ready: '待添加',
                    running: '正在添加',
                    success: '订阅成功',
                    conflict: '已存在 / 冲突',
                    failed: '添加失败',
                  } as any
                )[s.state]
              }}{{ s.message ? ' · ' + s.message : '' }}</small
            >
          </div>
          <label class="start"
            >从第几集开始<input
              v-model.number="s.start_episode"
              type="number"
              min="1"
              max="9999"
              :disabled="running || s.state === 'success' || s.state === 'conflict'"
          /></label>
        </div>
      </div>
      <div v-if="!rows.length" class="notice">没有可用的季度元数据，请返回刷新影视资料后重试。</div>
      <div class="button-row">
        <button :disabled="running" @click="emit('close', completed > 0)">
          {{ completed ? '完成' : '取消' }}</button
        ><button
          class="primary"
          :disabled="running || !checked.some((s) => s.state === 'ready' || s.state === 'failed')"
          @click="submit"
        >
          {{ running ? '正在逐季添加…' : completed ? '重试未成功的季' : '确认订阅所选季度' }}
        </button>
      </div>
      <p class="hint">每季结果单独保留。失败或冲突不会撤销已经创建成功的订阅。</p>
    </section>
  </div>
</template>
<style scoped>
.season-list {
  max-height: 55vh;
  overflow: auto;
  margin: 16px 0;
}
.season-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 0;
  border-bottom: 1px solid var(--border, #293347);
}
.season-row > input {
  width: 17px;
  height: 17px;
  flex: none;
}
.season-row img {
  width: 46px;
  height: 68px;
  object-fit: cover;
  border-radius: 5px;
}
.season-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.season-info small {
  color: var(--muted);
}
.start {
  font-size: 11px;
  max-width: 110px;
}
.start input {
  width: 100%;
  margin-top: 5px;
}
@media (max-width: 550px) {
  .season-row {
    gap: 8px;
  }
  .season-row img {
    display: none;
  }
  .start {
    max-width: 90px;
  }
}
</style>

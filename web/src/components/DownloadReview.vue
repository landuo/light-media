<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { api, action, ui, bytes, date, label, errorText, go } from '../api';
const props = defineProps<{ intent: any }>();
const emit = defineEmits<{ close: []; completed: [] }>();
const data = ref<any>(null),
  files = ref<any[]>([]),
  selected = ref<string[]>([]),
  media = ref<any>(props.intent.media),
  root = ref(props.intent.root_id || ''),
  mode = ref(props.intent.import_mode || ui.settings?.auto_import_mode || 'hardlink'),
  lookup = ref(''),
  lookupKind = ref(props.intent.media?.media_type || 'tv'),
  results = ref<any[]>([]),
  preview = ref<any>(null),
  targetConfirmed = ref(false),
  error = ref(''),
  loading = ref(false);
let identityGeneration = 0;
function ready(file: any) {
  return (
    !file.mapping_error &&
    !!file.application_path &&
    (file.progress >= 1 || (file.size > 0 && file.completed >= file.size))
  );
}
async function load() {
  loading.value = true;
  error.value = '';
  preview.value = null;
  try {
    data.value = await api('/downloads/' + encodeURIComponent(props.intent.id) + '/files');
    files.value = (data.value.files || []).map((file: any) => ({
      ...file,
      standard_season:
        file.parsed?.original?.form === 'season_episode' ? (file.parsed.original.season ?? '') : '',
      standard_episode:
        file.parsed?.original?.form === 'season_episode' &&
        file.parsed.original.episodes?.length === 1
          ? file.parsed.original.episodes[0]
          : '',
    }));
    selected.value = files.value
      .filter((f) => f.is_video && ready(f))
      .map((f) => f.application_path);
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
async function find() {
  results.value =
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
async function pick(item: any) {
  const generation = ++identityGeneration;
  const result = await action(() =>
    api('/catalog/detail?kind=' + item.media_type + '&id=' + item.id),
  );
  if (result && generation === identityGeneration) {
    media.value = result;
    results.value = [];
    preview.value = null;
    targetConfirmed.value = false;
  }
}
async function inspect() {
  preview.value = null;
  targetConfirmed.value = false;
  error.value = '';
  try {
    const chosen = files.value.filter((file) => selected.value.includes(file.application_path));
    if (!chosen.length || !media.value || !root.value)
      throw new Error('请确认影视、入库目录并选择已完成的视频文件');
    if (
      media.value.media_type === 'tv' &&
      chosen.some(
        (file) =>
          !Number.isInteger(+file.standard_season) ||
          +file.standard_season < 1 ||
          !Number.isInteger(+file.standard_episode) ||
          +file.standard_episode < 1,
      )
    )
      throw new Error('每个剧集文件必须人工明确一个标准季号与集号');
    preview.value = await api(
      '/downloads/' + encodeURIComponent(props.intent.id) + '/import-preview',
      {
        media_type: media.value.media_type,
        tmdb_id: media.value.id,
        root_id: root.value,
        mode: mode.value,
        files: chosen.map((file) => ({
          path: file.application_path,
          season: media.value.media_type === 'movie' ? 0 : +file.standard_season,
          episodes: media.value.media_type === 'movie' ? [] : [+file.standard_episode],
        })),
        mappings: props.intent.request?.mappings || [],
      },
    );
  } catch (e) {
    error.value = errorText(e);
  }
}
async function confirm() {
  error.value = '';
  try {
    const job = await api('/downloads/' + encodeURIComponent(props.intent.id) + '/import-confirm', {
      plan_id: preview.value.plan.id,
      confirm_target_change: targetConfirmed.value,
    });
    emit('completed');
    emit('close');
    go('jobs/' + encodeURIComponent(job.id));
  } catch (e) {
    error.value = errorText(e);
  }
}
onMounted(load);
</script>
<template>
  <div class="modal-backdrop">
    <section class="modal wide">
      <div class="section-heading">
        <h2>实际文件与人工纠错</h2>
        <button class="icon-button" @click="emit('close')">×</button>
      </div>
      <p class="notice">
        {{ intent.resource_title }}<br />{{ intent.id }} ·
        {{ label(data?.task?.state || intent.state) }}
      </p>
      <p v-if="intent.download_path" class="hint">下载目录：{{ intent.download_path }}</p>
      <p class="hint">
        此处用于已下载文件的人工纠错，可重新指定媒体库目标。读取下载器当前文件列表。只有下载完成且路径映射明确的视频可以整理；下载器任务和源文件始终保留。
      </p>
      <button @click="load" :disabled="loading || ui.busy > 0">重新读取实际文件</button>
      <p v-if="error" class="error-box">{{ error }}</p>
      <p v-if="loading">正在读取下载器…</p>
      <div v-if="data" class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>选择</th>
              <th>实际文件 / 应用路径</th>
              <th>大小 / 完成度</th>
              <th>标准季集</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(file, index) in files" :key="index">
              <td>
                <input
                  v-if="file.is_video"
                  type="checkbox"
                  v-model="selected"
                  :value="file.application_path"
                  :disabled="!ready(file)"
                  @change="preview = null"
                />
              </td>
              <td class="path-cell">
                {{ file.path }}<small>{{ file.application_path || '路径尚未映射' }}</small
                ><small v-if="file.mapping_error" class="error-text">{{ file.mapping_error }}</small
                ><small>{{ file.parsed?.reason }}</small>
              </td>
              <td>
                {{ bytes(file.size) }}<small>{{ Math.round((file.progress || 0) * 100) }}%</small>
              </td>
              <td>
                <template v-if="file.is_video && media?.media_type === 'tv'"
                  ><label
                    >标准季<input
                      v-model.number="file.standard_season"
                      type="number"
                      min="1"
                      @input="preview = null" /></label
                  ><label
                    >标准单集<input
                      v-model.number="file.standard_episode"
                      type="number"
                      min="1"
                      @input="preview = null" /></label></template
                ><span v-else>{{ file.is_video ? '电影' : '旁车 / 其他文件' }}</span>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-if="!files.length" class="muted">下载器当前没有返回文件。</p>
      </div>
      <h3>确认目标身份</h3>
      <p v-if="media" class="notice">
        {{ media.title }} · {{ media.year }} · {{ label(media.media_type) }} · TMDB {{ media.id }}
      </p>
      <details>
        <summary>作品识别有误，重新选择 TMDB 身份</summary>
        <form class="inline-form" @submit.prevent="find">
          <select v-model="lookupKind">
            <option value="movie">电影</option>
            <option value="tv">剧集</option></select
          ><input v-model="lookup" required placeholder="搜索正确作品" /><button>搜索 TMDB</button>
        </form>
        <div class="lookup-list">
          <button
            v-for="item in results"
            :key="item.media_type + ':' + item.id"
            @click="pick(item)"
          >
            {{ item.title }} · {{ item.year }} · TMDB {{ item.id }}
          </button>
        </div>
      </details>
      <div class="form-grid">
        <label
          >入库目录<select v-model="root" @change="preview = null">
            <option value="">请选择</option>
            <option v-for="r in ui.settings?.roots || []" :key="r.id" :value="r.id">
              {{ r.name || r.path }}
            </option>
          </select></label
        ><label
          >整理方式<select v-model="mode" @change="preview = null">
            <option value="hardlink">硬链接</option>
            <option value="copy">复制</option>
            <option value="auto">自动（允许复制回退）</option>
          </select></label
        >
      </div>
      <button
        class="primary"
        @click="inspect"
        :disabled="loading || ui.busy > 0 || !selected.length || !root || !media"
      >
        生成关联整理预览
      </button>
      <section v-if="preview" class="panel">
        <h3>核对发布计划</h3>
        <p class="hint">{{ preview.plan.id }} · 预览有效至 {{ date(preview.expires_at) }}</p>
        <p>
          预计占用 {{ bytes(preview.plan.space_needed) }} · 可用
          {{ bytes(preview.plan.space_available) }}
        </p>
        <p v-for="warning in preview.plan.warnings || []" :key="warning" class="warning">
          {{ warning }}
        </p>
        <div v-for="file in preview.plan.files || []" :key="file.id" class="timeline-entry">
          <p class="path-cell">{{ file.source }}<br />→ {{ file.target }}</p>
          <small
            >{{ label(file.method) }} · {{ label(file.status) }} · {{ bytes(file.size) }}</small
          >
          <p v-if="file.reason" class="error-text">{{ file.reason }}</p>
        </div>
        <div
          v-if="
            preview.identity_changed || preview.target_changed || preview.subscription_will_pause
          "
          class="warning"
        >
          <p>
            原目标：{{ preview.previous_media?.title }} · S{{ preview.previous_season }} /
            {{ (preview.previous_episodes || []).join(', ') }}
          </p>
          <p>
            目标身份、季集或入库位置已变化。关联订阅可能暂停，原集占位将按服务端计划转移；已发布资产不能借此静默改名。
          </p>
          <label class="check"
            ><input
              v-model="targetConfirmed"
              type="checkbox"
            />我已核对变更，并确认相关订阅暂停与占位调整</label
          >
        </div>
        <button
          class="primary"
          @click="confirm"
          :disabled="
            ui.busy > 0 ||
            !preview.plan.files?.length ||
            preview.plan.files?.some((f: any) => f.status === 'blocked' || !!f.reason) ||
            ((preview.identity_changed ||
              preview.target_changed ||
              preview.subscription_will_pause) &&
              !targetConfirmed)
          "
        >
          确认执行关联整理
        </button>
      </section>
    </section>
  </div>
</template>

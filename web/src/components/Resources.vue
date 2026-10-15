<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import {
  api,
  action,
  ui,
  emptyPolicy,
  episodeNumbers,
  bytes,
  label,
  date,
  go,
  friendly,
  searchStream,
  toast,
  errorText,
  materialLabel,
} from '../api';
import Policy from './Policy.vue';
import MappingRules from './MappingRules.vue';
const props = defineProps<{ route: string }>();
const [path, qs] = props.route.split('?'),
  parts = path!.split('/'),
  q = new URLSearchParams(qs),
  kind = parts[1]!,
  id = Number(parts[2]),
  season = ref(kind === 'movie' ? 0 : Number(q.get('season') || 1)),
  eps = ref(q.get('episodes') || ''),
  policy = ref(emptyPolicy()),
  selectedSites = ref<string[]>([]),
  output = ref<any>(null),
  media = ref<any>(null),
  page = ref(1),
  showRejected = ref(false),
  preview = ref<any>(null),
  extraAccepted = ref(false),
  downloader = ref(''),
  root = ref(''),
  mappings = ref<any[]>([]),
  mappingsLoading = ref(kind === 'tv'),
  mappingsError = ref(''),
  loading = ref(false);
async function loadMappings() {
  if (kind !== 'tv') return;
  mappingsLoading.value = true;
  mappingsError.value = '';
  try {
    const response = await api('/subscriptions');
    const subscriptions = Array.isArray(response) ? response : response?.items || [];
    const unique = new Map<string, any>();
    for (const subscription of subscriptions) {
      if (subscription.media?.media_type !== kind || Number(subscription.media?.id) !== id)
        continue;
      for (const rule of subscription.mappings || []) {
        if (rule.confirmed && rule.media_id === kind + ':' + id)
          unique.set(JSON.stringify(rule), rule);
      }
    }
    mappings.value = [...unique.values()];
  } catch (error) {
    mappingsError.value = errorText(error);
  } finally {
    mappingsLoading.value = false;
  }
}
let searchController: AbortController | null = null;
async function search() {
  searchController?.abort();
  const controller = new AbortController();
  searchController = controller;
  loading.value = true;
  output.value = null;
  try {
    const episodes = episodeNumbers(eps.value);
    await searchStream(
      {
        media_type: kind,
        tmdb_id: id,
        season: season.value,
        episodes,
        policy: policy.value,
        mappings: mappings.value.filter((rule) => rule.confirmed),
        site_ids: selectedSites.value,
        page: page.value,
      },
      (value) => {
        if (searchController === controller) output.value = { candidates: [], sites: [], ...value };
      },
      controller.signal,
    );
  } catch (e) {
    if (controller.signal.aborted) toast('本次搜索已取消，保留已完成站点的结果');
    else toast(errorText(e), true);
  } finally {
    if (searchController === controller) {
      loading.value = false;
      searchController = null;
    }
  }
}
function cancelSearch() {
  searchController?.abort();
}
onUnmounted(() => searchController?.abort());
async function inspect(c: any) {
  preview.value = null;
  extraAccepted.value = false;
  preview.value = await action(() => {
    const episodes = episodeNumbers(eps.value);
    return api('/downloads/preview', {
      media_type: kind,
      tmdb_id: id,
      season: season.value,
      episodes,
      site_id: c.resource.site_id,
      resource_id: c.resource.id,
      downloader_id: downloader.value,
      root_id: root.value,
      policy: policy.value,
      mappings: mappings.value.filter((rule) => rule.confirmed),
      page: page.value,
      workflow_id: output.value?.workflow_id || '',
    });
  });
}
async function confirm() {
  const r = await action(
    () =>
      api('/downloads/confirm', {
        preview_id: preview.value.id,
        accept_extra_files: extraAccepted.value,
      }),
    '下载任务已提交',
  );
  if (r) {
    preview.value = null;
    go('downloads');
  }
}
onMounted(async () => {
  void loadMappings();
  media.value = await action(() => api('/catalog/detail?kind=' + kind + '&id=' + id));
  downloader.value = ui.settings?.default_downloader || ui.settings?.downloaders?.[0]?.id || '';
  root.value = ui.settings?.default_root || ui.settings?.roots?.[0]?.id || '';
  selectedSites.value = (ui.settings?.sites || [])
    .filter((s: any) => s.enabled)
    .map((s: any) => s.id);
});
</script>
<template>
  <div class="page-heading">
    <div>
      <p class="eyebrow">RESOURCE SEARCH</p>
      <h1>{{ media?.title || '搜索资源' }}</h1>
      <p class="muted">先筛选，再核对真实种子文件，确认后才会发送到下载器。</p>
    </div>
    <button @click="go('detail/' + kind + '/' + id)">返回详情</button>
  </div>
  <section class="panel">
    <div class="form-grid">
      <label v-if="kind === 'tv'">季<input type="number" min="0" v-model.number="season" /></label
      ><label v-if="kind === 'tv'"
        >集数<input v-model="eps" placeholder="留空跟随已知季集，或 1,2,5-8" /></label
      ><label
        >下载器<select v-model="downloader">
          <option v-for="d in ui.settings?.downloaders || []" :key="d.id" :value="d.id">
            {{ d.name || d.id }}
          </option>
        </select></label
      ><label
        >入库目录<select v-model="root">
          <option v-for="r in ui.settings?.roots || []" :key="r.id" :value="r.id">
            {{ r.name || r.path }}
          </option>
        </select></label
      >
    </div>
    <details>
      <summary>资源筛选规则</summary>
      <Policy :model="policy" />
    </details>
    <div class="site-selector">
      <label v-for="s in ui.settings?.sites || []" :key="s.id" class="check"
        ><input v-model="selectedSites" type="checkbox" :value="s.id" />{{ s.name || s.id }}</label
      >
    </div>
    <div class="button-row">
      <button
        class="primary"
        @click="
          page = 1;
          search();
        "
        :disabled="loading || !selectedSites.length || mappingsLoading || !!mappingsError"
      >
        {{ loading ? '正在搜索…' : '搜索资源' }}</button
      ><button v-if="loading" @click="cancelSearch">取消本次搜索</button
      ><label class="check"><input v-model="showRejected" type="checkbox" />显示被筛除的资源</label>
    </div>
  </section>
  <p v-if="mappingsLoading" class="notice">正在载入这部作品已确认的季集映射…</p>
  <div v-if="mappingsError" class="error-box">
    无法载入已确认映射：{{ mappingsError }} <button @click="loadMappings">重试载入</button>
  </div>
  <details v-if="kind === 'tv' && !mappingsLoading && !mappingsError" class="panel">
    <summary>季集映射 · {{ mappings.filter((rule) => rule.confirmed).length }} 条已确认</summary>
    <p class="hint">
      已载入该作品订阅中的已确认规则。这里的修改用于本次搜索和下载预览；长期规则请在订阅中保存。重叠规则会由服务端拒绝，需人工核对。
    </p>
    <MappingRules :model="mappings" :media-id="kind + ':' + id" :season="season" />
  </details>
  <p v-if="loading" class="notice">
    正在按站点返回结果，可随时取消本次请求；已完成的站点结果会保留。
  </p>
  <div v-if="output">
    <div class="status-strip">
      <span v-for="s in output.sites" :key="s.id" :class="['badge', { bad: s.status !== 'ok' }]"
        >{{ s.id }} · {{ s.count }} 条 ·
        {{
          ({ ok: '正常', partial: '部分结果', empty: '没有结果', failed: '失败' } as any)[
            s.status
          ] || s.status
        }}<small v-if="s.message"> · {{ friendly(s.message) }}</small></span
      >
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>资源</th>
            <th>体积 / 规格</th>
            <th>活跃度</th>
            <th>促销</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(c, i) in output.candidates.filter(
              (x: any) => showRejected || x.accepted || x.needs_confirmation,
            )"
            :key="i"
            :class="{ dim: !c.accepted }"
          >
            <td class="resource-title">
              <strong>{{ c.resource.title }}</strong
              ><small>{{ c.resource.subtitle }}</small>
              <div class="tags">
                <span>{{ c.resource.site_id }}</span
                ><span v-if="c.season"
                  >S{{ c.season }}
                  {{ (c.episodes || []).map((e: number) => 'E' + e).join(' ') }}</span
                >
              </div>
              <p v-if="c.needs_confirmation" class="warning">
                需要人工复核：{{ c.reasons?.map(friendly).join('；') }}
              </p>
              <p v-else-if="!c.accepted" class="error-text">
                {{ c.reasons?.map(friendly).join('；') }}
              </p>
            </td>
            <td>
              {{ bytes(c.resource.size)
              }}<small
                >{{ c.resource.resolution }} · {{ c.resource.codec }} ·
                {{ c.resource.source }}</small
              >
            </td>
            <td>
              <span class="green"
                >↑ {{ c.resource.seeders < 0 ? '未知' : c.resource.seeders }}</span
              >
              / ↓ {{ c.resource.leechers < 0 ? '未知' : c.resource.leechers
              }}<small>{{ date(c.resource.published_at) }}</small>
            </td>
            <td>
              <span :class="['badge', { green: c.resource.free }]">{{
                c.resource.free ? 'FREE' : c.resource.download_factor + '×下载'
              }}</span
              ><small>{{ c.resource.upload_factor }}×上传</small
              ><small v-if="c.resource.promotion_end"
                >{{ date(c.resource.promotion_end) }} 截止</small
              >
            </td>
            <td>
              <button
                class="small primary"
                :disabled="
                  (!c.accepted && !c.needs_confirmation) ||
                  !downloader ||
                  !root ||
                  mappingsLoading ||
                  !!mappingsError
                "
                @click="inspect(c)"
              >
                预览下载
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="empty-state" v-if="!output.candidates?.length">
      <h3>本次未找到候选资源</h3>
      <p>查看各站点状态，或调整筛选规则后重试。</p>
    </div>
    <div class="pagination">
      <button
        :disabled="page <= 1 || loading"
        @click="
          page--;
          search();
        "
      >
        上一页</button
      ><span>第 {{ page }} 页</span
      ><button
        @click="
          page++;
          search();
        "
        :disabled="loading"
      >
        下一页
      </button>
    </div>
  </div>
  <div v-else-if="!loading" class="empty-state">
    <span>⌕</span>
    <h3>准备好后开始搜索</h3>
    <p>仅查询勾选的站点，站点失败会单独显示。</p>
  </div>
  <div v-if="preview" class="modal-backdrop" @click.self="preview = null">
    <section class="modal">
      <div class="section-heading">
        <h2>确认下载内容</h2>
        <button class="icon-button" @click="preview = null" aria-label="关闭">×</button>
      </div>
      <h3>{{ preview.resource_title }}</h3>
      <p class="muted">
        {{ preview.site_id }} · {{ bytes(preview.total_bytes) }} ·
        {{ preview.files?.length }} 个文件
      </p>
      <p class="hint">{{ materialLabel(preview) }}</p>
      <div class="notice">
        下载器：{{ preview.downloader_id }} · 入库目录：{{ preview.root_id }}<br />下载后整理：{{
          label(preview.import_mode)
        }}<br />预览有效至
        {{ date(preview.expires_at) }}
      </div>
      <div class="file-list">
        <div v-for="f in preview.files" :key="f">{{ f }}</div>
      </div>
      <div v-if="preview.warnings?.length" class="warning">
        <strong>下载前请核对</strong>
        <p v-for="(w, i) in preview.warnings" :key="i">{{ friendly(w) }}</p>
      </div>
      <label v-if="preview.needs_confirmation || preview.extra_files" class="check warning"
        ><input
          v-model="extraAccepted"
          type="checkbox"
        />我已核对上述复核提示，确认接受此资源包及额外内容。</label
      >
      <p class="hint">任务来源与 Info Hash 已由服务端核验。关闭窗口不会开始下载。</p>
      <div class="button-row">
        <button @click="preview = null">取消</button
        ><button
          class="primary"
          :disabled="
            ui.busy > 0 || ((preview.needs_confirmation || preview.extra_files) && !extraAccepted)
          "
          @click="confirm"
        >
          确认并开始下载
        </button>
      </div>
    </section>
  </div>
</template>

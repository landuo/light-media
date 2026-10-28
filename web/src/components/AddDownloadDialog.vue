<script setup lang="ts">
import { VSelect } from 'vuetify/components/VSelect';
import { VTextField } from 'vuetify/components/VTextField';
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { api, bytes, errorText, toast, ui } from '../api';
import AppIcon from './AppIcon.vue';
import DownloadTarget from './DownloadTarget.vue';
import type { Candidate } from '../features/resources';

const props = defineProps<{ candidate: Candidate; request?: any; media?: any }>();
const emit = defineEmits<{ close: []; done: [result: any]; error: [message: string] }>();
const downloader = ref(''),
  downloadPath = ref(''),
  advanced = ref(false),
  busy = ref(false),
  error = ref('');
const mediaType = ref(props.media?.media_type || 'movie'),
  tmdbID = ref<number | undefined>(props.media?.id);
const siteName = computed(
  () =>
    ui.settings?.sites?.find((s: any) => s.id === props.candidate.resource.site_id)?.name ||
    props.candidate.resource.site_id,
);
watch(
  () => props.candidate,
  () => {
    error.value = '';
  },
);
let disposed = false;
let releaseWait: (() => void) | undefined;
onBeforeUnmount(() => {
  disposed = true;
  releaseWait?.();
});
function waitForUpdate() {
  return new Promise<void>((resolve) => {
    const timer = setTimeout(() => {
      releaseWait = undefined;
      resolve();
    }, 250);
    releaseWait = () => {
      clearTimeout(timer);
      releaseWait = undefined;
      resolve();
    };
  });
}
async function waitForAcceptance(result: any) {
  if (!result?.id) throw new Error('未能确认下载结果');
  let current = result;
  while (!disposed) {
    if (current.state === 'accepted') return current;
    if (['failed', 'waiting', 'cancelled'].includes(current.state)) {
      const message = current.error || '添加下载失败';
      toast(`${siteName.value} ${props.candidate.resource.title} 添加下载失败：${message}`, true);
      emit('error', message);
      return;
    }
    if (!['planned', 'submitting'].includes(current.state)) throw new Error('未能确认下载状态');
    await waitForUpdate();
    if (disposed) return;
    current = await api('/downloads/native/' + encodeURIComponent(result.id));
  }
}
async function start() {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    const result = await api('/downloads/add', {
      site_id: props.candidate.resource.site_id,
      resource_id: props.candidate.resource.id,
      query: props.request?.query || '',
      page: props.request?.page || 1,
      browse: props.request?.browse === true,
      downloader_id: downloader.value,
      download_path: downloadPath.value,
      ...(tmdbID.value ? { media_type: mediaType.value, tmdb_id: tmdbID.value } : {}),
    });
    const accepted = await waitForAcceptance(result);
    if (accepted && !disposed) emit('done', accepted);
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div class="modal-backdrop" @click.self="!busy && emit('close')">
    <form
      class="modal download-dialog"
      role="dialog"
      aria-modal="true"
      aria-label="确认下载"
      @submit.prevent="start"
    >
      <div class="section-heading">
        <AppIcon name="download" />
        <div>
          <h2>确认下载</h2>
          <p class="hint">{{ siteName }}{{ media?.title ? ' - ' + media.title : '' }}</p>
        </div>
        <button
          type="button"
          class="icon-button"
          aria-label="关闭"
          :disabled="busy"
          @click="emit('close')"
        >
          <AppIcon name="close" />
        </button>
      </div>
      <div class="download-dialog-body">
        <div class="download-resource">
          <AppIcon name="globe" />
          <h3>{{ candidate.resource.title }}</h3>
          <span class="seeders">↑ {{ candidate.resource.seeders ?? '—' }}</span
          ><span class="peers">↓ {{ candidate.resource.leechers ?? '—' }}</span>
        </div>
        <p v-if="candidate.resource.subtitle" class="muted">{{ candidate.resource.subtitle }}</p>
        <p>
          <span class="badge">{{ bytes(candidate.resource.size) }}</span>
        </p>
        <DownloadTarget v-model:downloader="downloader" v-model:download-path="downloadPath" />
        <button
          type="button"
          class="advanced-toggle"
          :aria-expanded="advanced"
          @click="advanced = !advanced"
        >
          <AppIcon :name="advanced ? 'chevron-up' : 'chevron-down'" />
          {{ advanced ? '收起高级选项' : '高级选项' }}
        </button>
        <div v-if="advanced" class="form-grid advanced-download">
          <VSelect
            v-model="mediaType"
            label="媒体类型"
            :items="[
              { title: '电影', value: 'movie' },
              { title: '电视剧', value: 'tv' },
            ]"
          />
          <VTextField
            v-model.number="tmdbID"
            type="number"
            min="1"
            placeholder="辅助识别，可不填"
            label="TMDB ID"
          />
        </div>
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      </div>
      <div class="download-submit">
        <button class="primary" :disabled="busy">
          <AppIcon name="download" />{{ busy ? '正在添加下载…' : '开始下载' }}
        </button>
      </div>
    </form>
  </div>
</template>
<style scoped>
.download-dialog {
  max-width: 560px;
  padding: 0;
}
.download-dialog .section-heading {
  display: flex;
  gap: 12px;
  padding: 12px 24px;
  border-bottom: 1px solid var(--line);
  margin: 0;
}
.section-heading > svg {
  color: var(--muted);
  width: 24px;
  height: 24px;
}
.section-heading > div {
  flex: 1;
  min-width: 0;
}
.section-heading h2 {
  font-size: 20px;
  font-weight: 500;
}
.section-heading .hint {
  font-size: 14px;
  margin: 4px 0 0;
}
.download-dialog-body {
  padding: 24px;
}
.download-resource {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
}
.download-resource h3 {
  margin: 0;
  flex: 1;
  font-size: 16px;
  font-weight: 400;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.seeders {
  color: var(--success);
  font-size: 14px;
}
.peers {
  color: var(--warning);
  font-size: 14px;
}
.advanced-download {
  margin-top: 16px;
}
.advanced-toggle {
  border: 0;
  background: transparent;
  color: var(--accent);
  padding: 8px 0;
}
.download-submit {
  display: flex;
  justify-content: center;
  padding: 0 24px 24px;
}
.advanced-download input,
.advanced-download select {
  border: 0;
  border-bottom: 1px solid var(--line);
  border-radius: 0;
  background: transparent;
}
</style>

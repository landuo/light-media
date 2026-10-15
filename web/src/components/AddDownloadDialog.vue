<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { api, bytes, errorText, ui } from '../api';
import DownloadTarget from './DownloadTarget.vue';
import type { Candidate } from '../features/resources';

const props = defineProps<{ candidate: Candidate; request?: any; media?: any }>();
const emit = defineEmits<{ close: []; done: [result: any] }>();
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
    emit('done', result);
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
          ×
        </button>
      </div>
      <div class="download-resource">
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
        class="small ghost"
        :aria-expanded="advanced"
        @click="advanced = !advanced"
      >
        {{ advanced ? '收起高级选项' : '高级选项' }}
      </button>
      <div v-if="advanced" class="form-grid advanced-download">
        <label
          >媒体类型<select v-model="mediaType">
            <option value="movie">电影</option>
            <option value="tv">电视剧</option>
          </select></label
        >
        <label
          >TMDB ID<input
            v-model.number="tmdbID"
            type="number"
            min="1"
            placeholder="辅助识别，可不填"
        /></label>
      </div>
      <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      <div class="button-row">
        <button type="button" :disabled="busy" @click="emit('close')">取消</button
        ><button class="primary" :disabled="busy">{{ busy ? '正在添加下载…' : '开始下载' }}</button>
      </div>
    </form>
  </div>
</template>
<style scoped>
.download-dialog {
  max-width: 560px;
}
.download-resource {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.download-resource h3 {
  margin: 0;
  flex: 1 1 100%;
  overflow-wrap: anywhere;
}
.seeders {
  color: var(--success);
  font-size: 12px;
}
.peers {
  color: var(--warning);
  font-size: 12px;
}
.advanced-download {
  margin-top: 16px;
}
</style>

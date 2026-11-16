<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardItem, VCardTitle, VCardSubtitle, VCardText } from 'vuetify/components/VCard';
import { VDivider } from 'vuetify/components/VDivider';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { action, api } from '../api';

const props = defineProps<{ subid: string; season: number; title?: string }>();
const emit = defineEmits<{ close: [] }>();
const data = ref<any>(null);
const loading = ref(true);
const labels: Record<string, string> = {
  library: '已入库',
  downloading: '下载中',
  missing: '缺集',
  unaired: '未播',
  excluded: '已排除',
};
function size(value: number) {
  if (!value) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let n = value;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return n.toFixed(n >= 10 || i === 0 ? 0 : 1) + ' ' + units[i];
}
onMounted(async () => {
  data.value = await action(() =>
    api('/subscriptions/' + props.subid + '/files?season=' + (props.season || 0)),
  );
  loading.value = false;
});
</script>

<template>
  <VThemeProvider theme="light">
    <VDialog
      :model-value="true"
      scrollable
      max-width="60rem"
      content-class="plugin-dialog-surface"
      @update:model-value="!$event && emit('close')"
    >
      <VCard>
        <VCardItem>
          <template #prepend><VIcon icon="mdi-file-multiple" /></template>
          <VCardTitle>文件详情</VCardTitle>
          <VCardSubtitle>
            {{ title || data?.media?.title || '' }}
            {{ season ? ' 第 ' + season + ' 季' : '' }}
          </VCardSubtitle>
          <template #append>
            <VBtn icon variant="text" aria-label="关闭文件详情" @click="emit('close')">
              <VIcon icon="mdi-close" />
            </VBtn>
          </template>
        </VCardItem>
        <VDivider />
        <VCardText>
          <p v-if="loading" class="files-loading">正在读取文件信息…</p>
          <template v-else-if="data">
            <div class="files-summary">
              <span>共 {{ data.summary.total }} 集</span>
              <span>已入库 {{ data.summary.library }}</span>
              <span>下载中 {{ data.summary.downloading }}</span>
              <span>缺集 {{ data.summary.missing }}</span>
              <span v-if="data.summary.unaired">未播 {{ data.summary.unaired }}</span>
              <span v-if="data.summary.excluded">已排除 {{ data.summary.excluded }}</span>
            </div>
            <table class="files-table">
              <thead>
                <tr>
                  <th>集</th>
                  <th>标题</th>
                  <th>状态</th>
                  <th>文件</th>
                  <th>下载</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in data.episodes" :key="item.episode">
                  <td>{{ item.episode || '整部' }}</td>
                  <td>
                    <div class="files-title">{{ item.title || '第 ' + item.episode + ' 集' }}</div>
                    <div v-if="item.air_date" class="files-sub">{{ item.air_date }}</div>
                  </td>
                  <td>
                    <span :class="['files-state', item.status]">{{ labels[item.status] || item.status }}</span>
                  </td>
                  <td>
                    <div v-for="file in item.files" :key="file.path" class="files-row">
                      <span class="path-cell">{{ file.path }}</span>
                      <span v-if="file.size" class="files-sub">{{ size(file.size) }}</span>
                    </div>
                    <span v-if="!item.files.length" class="files-sub">—</span>
                  </td>
                  <td>
                    <div v-for="dl in item.downloads" :key="dl.hash + dl.title" class="files-row">
                      <span>{{ dl.title || dl.hash }}</span>
                      <span class="files-sub">
                        {{ dl.site_id }}{{ dl.downloader_id ? ' · ' + dl.downloader_id : '' }}
                        {{ dl.state ? ' · ' + dl.state : '' }}
                      </span>
                    </div>
                    <span v-if="!item.downloads.length" class="files-sub">—</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </template>
        </VCardText>
      </VCard>
    </VDialog>
  </VThemeProvider>
</template>

<style scoped>
.files-loading {
  color: var(--lm-muted, #6b7280);
  padding: 12px 0;
}
.files-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  margin-bottom: 12px;
  color: var(--lm-muted, #6b7280);
  font-size: 13px;
}
.files-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.files-table th,
.files-table td {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid rgba(0, 0, 0, 0.08);
  vertical-align: top;
}
.files-table th {
  color: var(--lm-muted, #6b7280);
  font-weight: 600;
  white-space: nowrap;
}
.files-title {
  font-weight: 600;
}
.files-sub {
  color: var(--lm-muted, #6b7280);
  font-size: 12px;
}
.files-row {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-bottom: 4px;
}
.files-state {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  background: rgba(0, 0, 0, 0.06);
  white-space: nowrap;
}
.files-state.library {
  background: rgba(76, 175, 80, 0.16);
  color: #2e7d32;
}
.files-state.downloading {
  background: rgba(33, 150, 243, 0.16);
  color: #1565c0;
}
.files-state.missing {
  background: rgba(255, 152, 0, 0.18);
  color: #e65100;
}
.files-state.excluded {
  background: rgba(158, 158, 158, 0.2);
  color: #616161;
}
</style>

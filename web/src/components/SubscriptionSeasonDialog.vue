<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardItem, VCardTitle, VCardSubtitle, VCardText } from 'vuetify/components/VCard';
import { VDivider } from 'vuetify/components/VDivider';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { action, api } from '../api';

const props = defineProps<{ subid: string; title?: string }>();
const emit = defineEmits<{ close: []; files: [season: number] }>();
const data = ref<any>(null);
const loading = ref(true);
onMounted(async () => {
  data.value = await action(() => api('/subscriptions/' + props.subid + '/seasons'));
  loading.value = false;
});
</script>

<template>
  <VThemeProvider theme="light">
    <VDialog
      :model-value="true"
      scrollable
      max-width="40rem"
      content-class="plugin-dialog-surface"
      @update:model-value="!$event && emit('close')"
    >
      <VCard>
        <VCardItem>
          <template #prepend><VIcon icon="mdi-television-classic" /></template>
          <VCardTitle>季详情</VCardTitle>
          <VCardSubtitle>{{ title || data?.media?.title || '' }}</VCardSubtitle>
          <template #append>
            <VBtn icon variant="text" aria-label="关闭季详情" @click="emit('close')">
              <VIcon icon="mdi-close" />
            </VBtn>
          </template>
        </VCardItem>
        <VDivider />
        <VCardText>
          <p v-if="loading" class="season-loading">正在读取季信息…</p>
          <table v-else-if="data" class="season-table">
            <thead>
              <tr>
                <th>季</th>
                <th>集数</th>
                <th>已入库</th>
                <th>下载中</th>
                <th>缺集</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in data.seasons" :key="row.season">
                <td>
                  <span class="season-name">{{ row.name || '第 ' + row.season + ' 季' }}</span>
                  <span v-if="row.subscribed" class="season-badge">已订阅</span>
                </td>
                <td>{{ row.episodes || '—' }}</td>
                <td>{{ row.library }}</td>
                <td>{{ row.downloading }}</td>
                <td>{{ row.subscribed ? row.missing : '—' }}</td>
                <td class="season-action">
                  <VBtn
                    size="small"
                    variant="text"
                    color="primary"
                    @click="emit('files', row.season)"
                    >文件</VBtn
                  >
                </td>
              </tr>
            </tbody>
          </table>
        </VCardText>
      </VCard>
    </VDialog>
  </VThemeProvider>
</template>

<style scoped>
.season-loading {
  color: var(--lm-muted, #6b7280);
  padding: 12px 0;
}
.season-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.season-table th,
.season-table td {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid rgba(0, 0, 0, 0.08);
}
.season-table th {
  color: var(--lm-muted, #6b7280);
  font-weight: 600;
  white-space: nowrap;
}
.season-name {
  font-weight: 600;
}
.season-badge {
  margin-left: 8px;
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 12px;
  background: rgba(103, 58, 183, 0.12);
  color: #5e35b1;
}
.season-action {
  text-align: right;
}
</style>

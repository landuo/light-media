<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import Draggable from 'vuedraggable';
import { VCard, VCardTitle, VCardSubtitle, VCardText } from 'vuetify/components/VCard';
import { VTextField } from 'vuetify/components/VTextField';
import { VSwitch } from 'vuetify/components/VSwitch';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText, toast } from '../api';
import { boundedRead } from '../features/bounded-read';

type EpisodeRule = {
  name: string;
  enabled: boolean;
  order: number;
  pattern: string;
  min_file_size_mb: number;
};
const rules = ref<(EpisodeRule & { localID: number })[]>([]);
const revision = ref(0),
  loaded = ref(false),
  loading = ref(false),
  saving = ref(false),
  error = ref('');
let sequence = 0;
let alive = true;
const read = new AbortController();
async function load() {
  if (loading.value || saving.value) return;
  loading.value = true;
  error.value = '';
  try {
    const result = await boundedRead(
      (signal) => api('/files/episode-format/rules', undefined, 'GET', signal),
      read.signal,
    );
    if (!alive) return;
    revision.value = result.revision;
    rules.value = [...result.rules]
      .sort((a, b) => a.order - b.order)
      .map((rule) => ({ ...rule, localID: ++sequence }));
    loaded.value = true;
  } catch (e) {
    if (alive) error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
function add() {
  if (!loaded.value || saving.value) return;
  rules.value.push({
    localID: ++sequence,
    name: '',
    enabled: true,
    order: rules.value.length + 1,
    pattern: '',
    min_file_size_mb: 500,
  });
}
async function save() {
  if (!loaded.value || saving.value) return;
  error.value = '';
  if (
    rules.value.some(
      (rule) =>
        !rule.name.trim() ||
        !rule.pattern ||
        !Number.isInteger(Number(rule.min_file_size_mb)) ||
        Number(rule.min_file_size_mb) < 0,
    )
  ) {
    error.value = '请填写规则名称、正则表达式及不小于0的整数文件大小';
    return;
  }
  saving.value = true;
  const payload = rules.value.map((rule, index) => ({
    name: rule.name,
    enabled: rule.enabled,
    order: index + 1,
    pattern: rule.pattern,
    min_file_size_mb: Number(rule.min_file_size_mb),
  }));
  try {
    const result = await api(
      '/files/episode-format/rules',
      { revision: revision.value, rules: payload },
      'PUT',
    );
    if (!alive) return;
    revision.value = result.revision;
    rules.value.forEach((rule, index) => {
      rule.order = index + 1;
    });
    toast('集数定位规则保存成功');
  } catch (e) {
    if (alive) error.value = errorText(e);
  } finally {
    saving.value = false;
  }
}
onMounted(load);
onUnmounted(() => {
  alive = false;
  read.abort();
});
</script>

<template>
  <VCard variant="flat" class="episode-rule-settings">
    <div class="episode-rule-header">
      <div>
        <VCardTitle>集数定位规则</VCardTitle
        ><VCardSubtitle>按顺序匹配文件名，为手动整理生成集数定位模板</VCardSubtitle>
      </div>
      <VBtn color="success" prepend-icon="mdi-plus" :disabled="!loaded || saving" @click="add"
        >添加规则</VBtn
      >
    </div>
    <VCardText>
      <VAlert v-if="error" type="error" variant="tonal" class="mb-4"
        >{{ error }}
        <VBtn variant="text" :disabled="loading || saving" @click="load">重新载入</VBtn></VAlert
      >
      <p v-if="loading" role="status">加载中…</p>
      <Draggable
        v-if="loaded"
        v-model="rules"
        item-key="localID"
        handle=".episode-rule-drag"
        :disabled="saving"
        @end="save"
      >
        <template #item="{ element, index }">
          <VCard variant="outlined" class="episode-rule-card">
            <VCardText class="episode-rule-row">
              <VBtn
                icon="mdi-drag"
                variant="text"
                size="small"
                class="episode-rule-drag"
                aria-label="拖动规则排序"
                :disabled="saving"
              />
              <VSwitch
                v-model="element.enabled"
                color="primary"
                hide-details
                density="compact"
                :aria-label="'启用规则 ' + (index + 1)"
                :disabled="saving"
              />
              <VTextField
                v-model="element.name"
                label="规则名称"
                hide-details="auto"
                :disabled="saving"
              />
              <VTextField
                v-model="element.pattern"
                label="正则表达式"
                class="episode-rule-pattern"
                hide-details="auto"
                :disabled="saving"
              />
              <VTextField
                v-model.number="element.min_file_size_mb"
                label="最小文件大小（MB）"
                type="number"
                min="0"
                hide-details="auto"
                :disabled="saving"
              />
              <VBtn
                icon="mdi-delete"
                variant="text"
                size="small"
                color="error"
                :aria-label="'删除规则 ' + (index + 1)"
                :disabled="saving"
                @click="rules.splice(index, 1)"
              />
            </VCardText>
          </VCard>
        </template>
      </Draggable>
      <p v-if="loaded && !rules.length" class="text-medium-emphasis">尚未配置集数定位规则</p>
      <p class="episode-rule-help">
        正则须包含命名捕获组 <code>(?P&lt;ep&gt;\d+)</code> 或
        <code>(?&lt;ep&gt;\d+)</code
        >，其他命名捕获组转为同名占位符。拖动排序后自动保存。最小文件大小仅过滤视频，字幕和外挂音频仍参与验证。
      </p>
      <p class="episode-rule-help">
        优先匹配启用的规则，未命中时根据样本智能分析。规则表为空时，手动整理中的推荐按钮会提示先填写规则；已有规则全部停用时仍可智能分析。模板会校验集数及附加文件兼容性；例如
        <code>(?P&lt;title&gt;.+)\.E(?P&lt;ep&gt;\d+)\.(?P&lt;ext&gt;.+)</code>。
      </p>
      <VBtn
        prepend-icon="mdi-content-save"
        :disabled="!loaded || saving"
        :loading="saving"
        @click="save"
        >保存</VBtn
      >
    </VCardText>
  </VCard>
</template>

<style scoped>
.episode-rule-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-right: 16px;
  flex-wrap: wrap;
}
.episode-rule-card {
  margin-bottom: 12px;
}
.episode-rule-row {
  display: grid;
  grid-template-columns: 32px 48px minmax(120px, 1fr) minmax(180px, 2fr) minmax(140px, 1fr) 32px;
  align-items: center;
  gap: 12px;
}
.episode-rule-help {
  font-size: 13px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.episode-rule-drag {
  cursor: grab;
}
@media (max-width: 1100px) {
  .episode-rule-row {
    grid-template-columns: 32px 48px minmax(0, 1fr) 32px;
  }
  .episode-rule-pattern {
    grid-column: 1 / -1;
  }
  .episode-rule-row > :nth-child(5) {
    grid-column: 1 / 4;
  }
}
</style>

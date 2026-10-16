<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { VCard, VCardTitle, VCardSubtitle, VCardText } from 'vuetify/components/VCard';
import { VTextarea } from 'vuetify/components/VTextarea';
import { VBtn } from 'vuetify/components/VBtn';
import { VAlert } from 'vuetify/components/VAlert';
import { api, errorText, toast } from '../api';

type Configuration = {
  source_mode: 'rust' | 'legacy';
  release_groups: string[];
  customization: string[];
};
type Section = 'release_groups' | 'customization';
const saved = ref<Configuration>({ source_mode: 'rust', release_groups: [], customization: [] });
const revision = ref(0),
  loaded = ref(false),
  loading = ref(false),
  saving = ref<Section | null>(null);
const drafts = ref<Record<Section, string>>({ release_groups: '', customization: '' });
const loadError = ref('');
const issues = ref<Record<Section, string>>({ release_groups: '', customization: '' });
const sections = [
  {
    key: 'release_groups' as const,
    title: '自定义制作组/字幕组',
    description: '添加无法识别的制作组/字幕组。',
    hint: '支持正则表达式，特殊字符需要\\转义，一行代表一个制作组/字幕组',
    icon: 'mdi-account-group',
  },
  {
    key: 'customization' as const,
    title: '自定义占位符',
    description: '添加自定义占位符识别正则。',
    hint: '支持正则表达式，特殊字符需要\\转义，多个匹配对象请换行分隔',
    icon: 'mdi-code-braces',
  },
];
async function load() {
  loading.value = true;
  loadError.value = '';
  try {
    const result = await api('/settings/resource-metadata');
    saved.value = result.config;
    revision.value = result.revision;
    drafts.value = {
      release_groups: saved.value.release_groups.join('\n'),
      customization: saved.value.customization.join('\n'),
    };
    loaded.value = true;
  } catch (error) {
    loadError.value = errorText(error);
  } finally {
    loading.value = false;
  }
}
async function save(section: Section) {
  if (!loaded.value || saving.value) return;
  saving.value = section;
  issues.value[section] = '';
  // A card saves only its own text. An unsaved draft in the other card stays
  // local, and cannot accidentally enter this configuration update.
  const configuration = { ...saved.value, [section]: drafts.value[section].split('\n') };
  try {
    const result = await api(
      '/settings/resource-metadata',
      {
        revision: revision.value,
        config: configuration,
      },
      'PUT',
    );
    saved.value = configuration;
    revision.value = result.revision;
    toast(section === 'release_groups' ? '自定义制作组/字幕组保存成功' : '自定义占位符保存成功');
  } catch (error) {
    issues.value[section] = errorText(error);
  } finally {
    saving.value = null;
  }
}
onMounted(load);
</script>

<template>
  <div class="resource-metadata-settings">
    <VAlert v-if="loadError" type="error" variant="tonal"
      >{{ loadError }} <VBtn variant="text" @click="load">重试</VBtn></VAlert
    >
    <p v-if="loading" role="status">加载中…</p>
    <template v-if="loaded">
      <VCard v-for="section in sections" :key="section.key" variant="flat">
        <VCardTitle>{{ section.title }}</VCardTitle>
        <VCardSubtitle>{{ section.description }}</VCardSubtitle>
        <VCardText>
          <VTextarea
            v-model="drafts[section.key]"
            :aria-label="section.title"
            :placeholder="section.hint"
            :hint="section.hint"
            persistent-hint
            :prepend-inner-icon="section.icon"
          />
        </VCardText>
        <VCardText>
          <VAlert v-if="issues[section.key]" type="error" variant="tonal" class="mb-4">{{
            issues[section.key]
          }}</VAlert>
          <VBtn
            :loading="saving === section.key"
            :disabled="saving !== null"
            prepend-icon="mdi-content-save"
            @click="save(section.key)"
            >保存</VBtn
          >
        </VCardText>
      </VCard>
    </template>
  </div>
</template>

<style scoped>
.resource-metadata-settings {
  display: grid;
  gap: 24px;
  margin-top: 24px;
}
.resource-metadata-settings :deep(.v-field textarea) {
  border: 0;
  border-radius: 0;
  background: transparent;
  padding: var(--v-field-input-padding-top) var(--v-field-padding-end)
    var(--v-field-input-padding-bottom) var(--v-field-padding-start);
  box-shadow: none;
  color: inherit;
  font: inherit;
  line-height: 1.5;
}
.resource-metadata-settings :deep(.v-label) {
  flex-direction: row;
  gap: 0;
}
</style>

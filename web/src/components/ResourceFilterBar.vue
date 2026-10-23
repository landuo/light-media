<script setup lang="ts">
import { VTextField } from 'vuetify/components/VTextField';
import { computed, ref } from 'vue';
import { VMenu } from 'vuetify/components/VMenu';
import { VBtn } from 'vuetify/components/VBtn';
import { VList, VListItem } from 'vuetify/components/VList';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VChip } from 'vuetify/components/VChip';
import { VChipGroup } from 'vuetify/components/VChipGroup';
import { VIcon } from 'vuetify/components/VIcon';
import { useDisplay } from 'vuetify';
import AppIcon from './AppIcon.vue';
import {
  emptyResourceFilters,
  filterValues,
  resourceGroup,
  type Candidate,
  type ResourceFilters,
} from '../features/resources';
const props = defineProps<{
  candidates: Candidate[];
  count: number;
  names: Record<string, string>;
}>();
const filters = defineModel<ResourceFilters>('filters', { required: true });
const sort = defineModel<string>('sort', { required: true });
const descending = defineModel<boolean>('descending', { required: true });
const advanced = ref(false),
  single = ref(false),
  current = ref(0);
const display = useDisplay();
const fields = [
  { key: 'site', label: '站点', icon: 'mdi-server-network' },
  { key: 'season', label: '季', icon: 'mdi-television-classic' },
  { key: 'promotion', label: '促销状态', icon: 'mdi-gift-outline' },
  { key: 'codec', label: '视频编码', icon: 'mdi-video-vintage' },
  { key: 'source', label: '质量', icon: 'mdi-quality-high' },
  { key: 'resolution', label: '分辨率', icon: 'mdi-monitor-screenshot' },
  { key: 'group', label: '制作组', icon: 'mdi-account-group-outline' },
] as const;
const sortOptions = [
  { value: 'default', title: '默认' },
  { value: 'seeders', title: '做种人数' },
  { value: 'size', title: '体积' },
  { value: 'published_at', title: '发布时间' },
  { value: 'site', title: '站点' },
];
function options(key: string) {
  if (key === 'promotion')
    return [
      { value: 'free', title: '免费' },
      { value: 'discount', title: '下载折扣' },
      { value: 'upload', title: '上传加成' },
      { value: 'normal', title: '普通' },
      { value: 'unknown', title: '未知' },
    ];
  return [
    ...new Set(
      props.candidates
        .map((c) =>
          key === 'group'
            ? resourceGroup(c)
            : key === 'season'
              ? String(c.season || 0)
              : String(c.resource[key === 'site' ? 'site_id' : key] || ''),
        )
        .filter(Boolean),
    ),
  ]
    .sort()
    .map((value) => ({
      value,
      title:
        key === 'site'
          ? props.names[value] || value
          : key === 'season'
            ? value === '0'
              ? '未识别'
              : `第 ${value} 季`
            : value,
    }));
}
const selected = computed(() =>
  Object.values(filters.value).reduce((sum, value) => sum + filterValues(value).length, 0),
);
function selectAll(key: (typeof fields)[number]['key']) {
  filters.value[key] = options(key).map((option) => option.value);
}
</script>
<template>
  <div class="torrent-filter-bar">
    <span class="resource-count"><AppIcon name="search" />{{ count }} 个资源</span>
    <VMenu
      ><template #activator="{ props: activator }"
        ><VBtn v-bind="activator" variant="text" size="small" class="sort-control"
          ><AppIcon name="sort" />{{ sortOptions.find((o) => o.value === sort)?.title
          }}<AppIcon name="chevron-down" /></VBtn
      ></template>
      <VList density="compact"
        ><VListItem
          v-for="option in sortOptions"
          :key="option.value"
          :title="option.title"
          :active="sort === option.value"
          @click="sort = option.value" /><VListItem
          v-if="sort !== 'default'"
          :title="descending ? '改为升序' : '改为降序'"
          @click="descending = !descending"
      /></VList>
    </VMenu>
    <span class="filter-divider"></span>
    <VBtn
      v-for="(field, index) in fields"
      :key="field.key"
      variant="tonal"
      color="primary"
      rounded
      size="small"
      :aria-label="field.label"
      :class="{ active: filterValues(filters[field.key]).length }"
      @click="
        current = index;
        single = true;
      "
      ><VIcon :icon="field.icon" size="16" />{{ field.label
      }}{{
        filterValues(filters[field.key]).length
          ? ` · ${filterValues(filters[field.key]).length}`
          : ''
      }}</VBtn
    >
    <VBtn variant="tonal" color="primary" rounded size="small" @click="advanced = true"
      ><AppIcon name="filter" />综合筛选{{ selected ? ` · ${selected}` : '' }}</VBtn
    >
  </div>
  <VDialog v-model="single" max-width="25rem" max-height="85vh" scrollable>
    <VCard>
      <VCardTitle class="filter-dialog-title"
        ><VIcon :icon="fields[current]!.icon" /><span>{{ fields[current]!.label }}</span
        ><VBtn
          variant="text"
          size="small"
          class="push-right"
          @click="filters[fields[current]!.key] = []"
          >清空</VBtn
        ><VBtn variant="text" size="small" @click="selectAll(fields[current]!.key)"
          >全选</VBtn
        ></VCardTitle
      >
      <VCardText
        ><VChipGroup
          :model-value="filterValues(filters[fields[current]!.key])"
          multiple
          column
          @update:model-value="filters[fields[current]!.key] = $event"
          ><VChip
            v-for="option in options(fields[current]!.key)"
            :key="option.value"
            :value="option.value"
            filter
            size="small"
            variant="elevated"
            >{{ option.title }}</VChip
          ></VChipGroup
        ></VCardText
      >
      <VCardActions class="filter-dialog-actions"
        ><VBtn color="primary" variant="flat" class="push-right" @click="single = false"
          >确定</VBtn
        ></VCardActions
      >
    </VCard>
  </VDialog>
  <VDialog v-model="advanced" max-width="50rem" scrollable :fullscreen="!display.mdAndUp.value">
    <VCard>
      <VCardTitle class="filter-dialog-title"
        ><VIcon icon="mdi-filter-variant" /><span>综合筛选</span
        ><VBtn
          v-if="selected"
          variant="text"
          size="small"
          class="push-right"
          @click="filters = emptyResourceFilters()"
          >清空全部</VBtn
        ><VBtn
          icon="mdi-close"
          variant="text"
          size="small"
          class="push-right"
          aria-label="关闭综合筛选"
          @click="advanced = false"
      /></VCardTitle>
      <VCardText class="filter-dialog-body">
        <VTextField v-model="filters.text" placeholder="在结果中搜索标题或副标题" label="标题" />
        <div class="all-filters-grid">
          <VCard
            v-for="field in fields"
            :key="field.key"
            v-show="options(field.key).length"
            variant="tonal"
          >
            <VCardTitle class="filter-dialog-title"
              ><VIcon :icon="field.icon" size="20" /><span>{{ field.label }}</span
              ><VBtn variant="text" size="small" class="push-right" @click="selectAll(field.key)"
                >全选</VBtn
              ><VBtn
                v-if="filterValues(filters[field.key]).length"
                variant="text"
                size="small"
                color="error"
                @click="filters[field.key] = []"
                >清空</VBtn
              ></VCardTitle
            >
            <VCardText
              ><VChipGroup
                :model-value="filterValues(filters[field.key])"
                multiple
                column
                @update:model-value="filters[field.key] = $event"
                ><VChip
                  v-for="option in options(field.key)"
                  :key="option.value"
                  :value="option.value"
                  filter
                  variant="elevated"
                  size="small"
                  >{{ option.title }}</VChip
                ></VChipGroup
              ></VCardText
            >
          </VCard>
        </div>
      </VCardText>
    </VCard>
  </VDialog>
</template>
<style scoped>
.filter-dialog-title {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--line);
  font-size: 18px;
}
.filter-dialog-actions {
  padding: 12px 24px;
}
.filter-dialog-body {
  overflow-y: auto;
}
.all-filters-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(18rem, 1fr));
  gap: 16px;
  margin-top: 16px;
}
.torrent-filter-bar .sort-control {
  color: var(--muted) !important;
  background: transparent !important;
}

.torrent-filter-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding: 12px;
  margin-bottom: 12px;
  border-radius: 12px;
  background: var(--surface);
}
.torrent-filter-bar .v-btn {
  letter-spacing: 0;
  font-size: 12px;
  height: 30px;
  padding: 0 14px;
  min-width: 0;
}
.torrent-filter-bar :deep(.v-btn__content) {
  gap: 6px;
}
.torrent-filter-bar svg {
  width: 16px;
  height: 16px;
}
.resource-count {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: var(--accent);
  color: #fff;
  border-radius: 16px;
  padding: 4px 10px;
  font-size: 12px;
  white-space: nowrap;
}
.filter-divider {
  height: 24px;
  border-left: 1px solid var(--line);
}
.resource-filter-dialog {
  margin: 0;
  max-height: 85vh;
  overflow: auto;
}
.filter-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin: 16px 0;
}
.resource-filter-dialog h3 {
  margin-bottom: 20px;
}
.active {
  outline: 1px solid var(--accent);
}
</style>

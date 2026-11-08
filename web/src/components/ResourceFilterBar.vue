<script setup lang="ts">
import { VTextField } from 'vuetify/components/VTextField';
import { computed, ref } from 'vue';
import { VMenu } from 'vuetify/components/VMenu';
import { VBtn } from 'vuetify/components/VBtn';
import { VList, VListItem } from 'vuetify/components/VList';
import { VDivider } from 'vuetify/components/VDivider';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VChip } from 'vuetify/components/VChip';
import { VChipGroup } from 'vuetify/components/VChipGroup';
import { VIcon } from 'vuetify/components/VIcon';
import { useDisplay } from 'vuetify';
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
  { value: 'site', title: '站点' },
  { value: 'size', title: '体积' },
  { value: 'seeders', title: '做种人数' },
  { value: 'published_at', title: '发布时间' },
];
type FilterKey = (typeof fields)[number]['key'];
function options(key: FilterKey) {
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
const toolbarFields = computed(() =>
  fields
    .map((field, index) => ({
      ...field,
      index,
      options: options(field.key),
      selected: filterValues(filters.value[field.key]),
    }))
    .filter((field) => field.options.length || field.selected.length),
);
function selectAll(key: FilterKey) {
  filters.value[key] = options(key).map((option) => option.value);
}
function removeFilter(key: FilterKey, value: string) {
  filters.value[key] = filterValues(filters.value[key]).filter((item) => item !== value);
}
function openSingleFilter(index: number) {
  if (display.smAndUp.value) return;
  current.value = index;
  single.value = true;
}
</script>
<template>
  <VCard class="torrent-filter-bar" elevation="0">
    <div class="filter-toolbar">
      <div class="filter-sort">
        <VChip color="primary" variant="flat" size="small" prepend-icon="mdi-magnify">
          {{ count }} 个资源
        </VChip>
        <VMenu>
          <template #activator="{ props: activator }">
            <VBtn
              v-bind="activator"
              :prepend-icon="descending ? 'mdi-sort-descending' : 'mdi-sort-ascending'"
              append-icon="mdi-chevron-down"
              variant="text"
              size="small"
              class="sort-control"
              aria-label="排序"
            >
              {{ sortOptions.find((option) => option.value === sort)?.title }}
            </VBtn>
          </template>
          <VList density="compact" min-width="120">
            <VListItem
              title="升序"
              prepend-icon="mdi-sort-ascending"
              :active="!descending"
              color="primary"
              @click="descending = false"
            />
            <VListItem
              title="降序"
              prepend-icon="mdi-sort-descending"
              :active="descending"
              color="primary"
              @click="descending = true"
            />
            <VDivider class="my-1" />
            <VListItem
              v-for="option in sortOptions"
              :key="option.value"
              :title="option.title"
              :active="sort === option.value"
              color="primary"
              @click="sort = option.value"
            />
          </VList>
        </VMenu>
      </div>
      <span v-if="display.smAndUp.value" class="filter-divider"></span>
      <div class="filter-buttons">
        <VBtn
          v-for="field in toolbarFields"
          :key="field.key"
          :prepend-icon="field.icon"
          :color="!display.smAndUp.value || field.selected.length ? 'primary' : undefined"
          :variant="display.smAndUp.value ? 'tonal' : 'text'"
          :rounded="display.smAndUp.value ? 'pill' : undefined"
          :class="{ 'mobile-filter': !display.smAndUp.value }"
          :aria-label="field.label"
          size="small"
          @click="openSingleFilter(field.index)"
        >
          {{ field.label }}
          <VChip v-if="field.selected.length" color="primary" size="x-small" class="ms-1">
            {{ field.selected.length }}
          </VChip>
          <VMenu
            v-if="display.smAndUp.value"
            activator="parent"
            :close-on-content-click="false"
            scrim
          >
            <VCard max-width="20rem">
              <VCardText class="filter-menu-content">
                <div class="d-flex justify-space-between">
                  <VBtn variant="text" size="small" color="primary" @click="selectAll(field.key)">
                    全选
                  </VBtn>
                  <VBtn
                    v-if="field.selected.length"
                    variant="text"
                    size="small"
                    color="error"
                    @click="filters[field.key] = []"
                  >
                    清空
                  </VBtn>
                </div>
                <VChipGroup
                  :model-value="field.selected"
                  multiple
                  column
                  @update:model-value="filters[field.key] = $event"
                >
                  <VChip
                    v-for="option in field.options"
                    :key="option.value"
                    :value="option.value"
                    filter
                    color="primary"
                    size="small"
                    variant="elevated"
                  >
                    {{ option.title }}
                  </VChip>
                </VChipGroup>
              </VCardText>
            </VCard>
          </VMenu>
        </VBtn>
        <VBtn
          :variant="display.smAndUp.value ? 'tonal' : 'text'"
          :rounded="display.smAndUp.value ? 'pill' : undefined"
          :class="{ 'mobile-filter': !display.smAndUp.value }"
          prepend-icon="mdi-filter-variant"
          color="primary"
          size="small"
          @click="advanced = true"
        >
          综合筛选
          <VChip v-if="selected" color="primary" size="x-small" class="ms-1">
            {{ selected }}
          </VChip>
        </VBtn>
      </div>
    </div>
    <div v-if="selected" class="selected-filters">
      <div class="selected-filter-chips">
        <VChip
          v-if="filters.text"
          color="primary"
          variant="elevated"
          size="small"
          closable
          close-label="移除标题筛选"
          @click:close="filters.text = ''"
        >
          <strong>标题：</strong>{{ filters.text }}
        </VChip>
        <template v-for="field in toolbarFields" :key="field.key">
          <VChip
            v-for="value in field.selected"
            :key="value"
            :prepend-icon="field.icon"
            :close-label="`移除${field.label}筛选`"
            color="primary"
            variant="elevated"
            size="small"
            closable
            @click:close="removeFilter(field.key, value)"
          >
            <strong>{{ field.label }}：</strong>
            {{ field.options.find((option) => option.value === value)?.title ?? value }}
          </VChip>
        </template>
      </div>
      <VBtn
        prepend-icon="mdi-close-circle-outline"
        variant="text"
        size="small"
        color="error"
        @click="filters = emptyResourceFilters()"
      >
        清空全部
      </VBtn>
    </div>
  </VCard>
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
.torrent-filter-bar {
  margin-bottom: 12px;
  background: rgba(var(--v-theme-surface), 0.82);
}
.filter-toolbar,
.filter-sort {
  display: flex;
  align-items: center;
  gap: 6px;
}
.filter-toolbar {
  min-width: 0;
  padding: 12px;
}
.filter-sort {
  flex-shrink: 0;
  gap: 8px;
}
.filter-toolbar .v-btn {
  letter-spacing: 0;
}
.sort-control {
  color: var(--muted);
}
.filter-divider {
  align-self: center;
  flex: 0 0 1px;
  height: 24px;
  margin-inline: 4px;
  background: var(--line);
}
.filter-buttons {
  display: flex;
  align-items: center;
  flex: 1;
  min-width: 0;
  gap: 6px;
  overflow-x: auto;
  scrollbar-width: none;
}
.filter-buttons > .v-btn {
  flex-shrink: 0;
}
.filter-menu-content {
  max-height: 50vh;
  overflow-y: auto;
}
.selected-filters,
.selected-filter-chips {
  display: flex;
  align-items: center;
  gap: 6px;
}
.selected-filters {
  border-top: 1px solid var(--line);
  background: rgba(var(--v-theme-surface-variant), 0.05);
  padding: 7px 12px;
}
.selected-filter-chips {
  flex: 1;
  flex-wrap: wrap;
  min-width: 0;
}
@media (max-width: 599px) {
  .filter-toolbar {
    display: block;
    padding: 8px;
  }
  .filter-sort {
    justify-content: space-between;
  }
  .filter-buttons {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 4px;
    margin-top: 8px;
  }
  .mobile-filter {
    min-width: 0;
    min-height: 44px;
    border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
    border-radius: 8px;
    background: rgba(var(--v-theme-surface-variant), 0.08);
    padding-inline: 4px;
    font-size: 12px;
  }
  .mobile-filter :deep(.v-btn__prepend) {
    margin-inline: 0 3px;
  }
  .selected-filters {
    flex-wrap: wrap;
  }
}
</style>

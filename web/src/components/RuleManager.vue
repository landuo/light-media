<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardTitle, VCardText, VCardActions } from 'vuetify/components/VCard';
import { VRow, VCol } from 'vuetify/components/VGrid';
import { VBtn } from 'vuetify/components/VBtn';
import { VTextField } from 'vuetify/components/VTextField';
import { VTextarea } from 'vuetify/components/VTextarea';
import { VSelect } from 'vuetify/components/VSelect';
import { VAutocomplete } from 'vuetify/components/VAutocomplete';
import { VDivider } from 'vuetify/components/VDivider';
import { VAlert } from 'vuetify/components/VAlert';
import Draggable from 'vuedraggable';
import { api, errorText, toast } from '../api';
import { copyAPIToken as copyText } from '../features/api-token';
import {
  customRuleIssue,
  editRuleIssue,
  groupIssue,
  importRuleCode,
  ruleLevels,
  ruleExpression,
  nextRuleName,
  type CustomRule,
  type RuleGroup,
  type RuleSection,
} from '../features/priority-rules';
import { builtinRuleOptions } from '../features/priority-rule-options';
import AppIcon from './AppIcon.vue';
import filterImage from '../assets/rules/filter.svg';
import groupImage from '../assets/rules/filter-group.svg';

type Draft<T> = { key: number; value: T };
let serial = 0;
const wrap = <T,>(value: T): Draft<T> => ({ key: ++serial, value });
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value));
const custom = ref<Draft<CustomRule>[]>([]),
  groups = ref<Draft<RuleGroup>[]>([]),
  priority = ref<string[]>([]);
const revisions = ref<Record<RuleSection, number>>({ custom: 0, groups: 0, priority: 0 });
const categories = ref<Record<string, string[]>>({}),
  loading = ref(true),
  loadError = ref('');
const errors = ref<Record<RuleSection, string>>({ custom: '', groups: '', priority: '' });
const saving = ref<RuleSection | null>(null);
const display = useDisplay();
const customEdit = ref<{ key: number; original: string; value: CustomRule } | null>(null);
const groupEdit = ref<{ key: number; original: string; value: RuleGroup } | null>(null);
const levels = ref<Draft<string[]>[]>([]),
  editorError = ref('');
const importing = ref<'custom' | 'groups' | 'levels' | null>(null),
  code = ref(''),
  importError = ref('');
const options = computed(() => [
  ...builtinRuleOptions,
  ...custom.value.map((item) => ({ title: item.value.name, value: item.value.id })),
]);
const categoryOptions = computed(() => [
  { title: '全部', value: '' },
  ...(categories.value[groupEdit.value?.value.media_type || ''] || []).map((name) => ({
    title: name,
    value: name,
  })),
]);
const priorityOptions = [
  { title: '资源优先级', value: 'torrent' },
  { title: '站点优先级', value: 'site' },
  { title: '站点上传量', value: 'upload' },
  { title: '资源做种数', value: 'seeder' },
];
const customFields = [
  {
    key: 'id',
    label: '规则ID',
    cols: 6,
    hint: '字符与数字组合，不能含空格',
    placeholder: '必填；不可与其他规则ID重名',
  },
  {
    key: 'name',
    label: '规则名称',
    cols: 6,
    hint: '使用别名便于区分规则',
    placeholder: '必填；不可与其他规则名称重名',
  },
  {
    key: 'include',
    label: '包含',
    cols: 12,
    hint: '必须包含的关键字或正则表达式，多个值使用｜分隔',
    placeholder: '关键字/正则表达式',
  },
  {
    key: 'exclude',
    label: '排除',
    cols: 12,
    hint: '不能包含的关键字或正则表达式，多个值使用｜分隔',
    placeholder: '关键字/正则表达式',
  },
  {
    key: 'size_range',
    label: '资源体积（MB）',
    cols: 6,
    hint: '最小资源文件体积或体积范围（剧集计算单集平均大小）',
    placeholder: '0/1-10',
  },
  {
    key: 'seeders',
    label: '做种人数',
    cols: 6,
    hint: '最小做种人数或做种人数范围',
    placeholder: '0/1-10',
  },
  {
    key: 'publish_time',
    label: '发布时间（分钟）',
    cols: 6,
    hint: '距离资源发布的最小时间间隔或时间区间',
    placeholder: '0/1-10',
  },
] as const;
async function load() {
  loading.value = true;
  loadError.value = '';
  try {
    const result = await api('/settings/rules');
    custom.value = (result.configuration.custom_rules || []).map(wrap);
    groups.value = (result.configuration.groups || []).map(wrap);
    priority.value = result.configuration.torrent_priority || ['torrent', 'upload', 'seeder'];
    revisions.value = result.revisions;
    categories.value = result.categories;
  } catch (e) {
    loadError.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
async function save(section: RuleSection) {
  if (saving.value) return;
  const value = clone(
    section === 'custom'
      ? custom.value.map((item) => item.value)
      : section === 'groups'
        ? groups.value.map((item) => item.value)
        : priority.value,
  );
  const issue =
    section === 'custom'
      ? customRuleIssue(value as CustomRule[])
      : section === 'groups'
        ? groupIssue(value as RuleGroup[])
        : '';
  errors.value[section] = issue;
  if (issue) return;
  saving.value = section;
  try {
    const result = await api(
      '/settings/rules/' + section,
      { revision: revisions.value[section], value },
      'PUT',
    );
    revisions.value[section] = result.revision;
    toast(
      section === 'custom'
        ? '自定义规则已保存'
        : section === 'groups'
          ? '规则组已保存'
          : '下载规则已保存',
    );
  } catch (e) {
    errors.value[section] = errorText(e);
  } finally {
    saving.value = null;
  }
}
function addCustom() {
  custom.value.push(
    wrap({
      id: nextRuleName(
        'RULE',
        custom.value.map((item) => item.value.id),
      ),
      name: nextRuleName(
        '规则',
        custom.value.map((item) => item.value.name),
      ),
    }),
  );
}
function addGroup() {
  groups.value.push(
    wrap({
      name: nextRuleName(
        '规则组',
        groups.value.map((item) => item.value.name),
      ),
      media_type: '',
      category: '',
    }),
  );
}
function editCustom(item: Draft<CustomRule>) {
  editorError.value = '';
  customEdit.value = { key: item.key, original: item.value.id, value: clone(item.value) };
}
function confirmCustom() {
  if (!customEdit.value) return;
  const draft = customEdit.value;
  editorError.value = editRuleIssue(
    draft.value,
    custom.value.filter((item) => item.key !== draft.key).map((item) => item.value),
  );
  if (editorError.value) return;
  const item = custom.value.find((item) => item.key === draft.key);
  if (item) item.value = clone(draft.value);
  customEdit.value = null;
}
function editGroup(item: Draft<RuleGroup>) {
  editorError.value = '';
  groupEdit.value = { key: item.key, original: item.value.name, value: clone(item.value) };
  levels.value = ruleLevels(item.value.rule_string).map(wrap);
}
function confirmGroup() {
  if (!groupEdit.value) return;
  const draft = groupEdit.value;
  editorError.value = groupIssue([
    ...groups.value.filter((item) => item.key !== draft.key).map((item) => item.value),
    draft.value,
  ]);
  if (editorError.value) return;
  const item = groups.value.find((item) => item.key === draft.key);
  if (item)
    item.value = {
      ...clone(draft.value),
      rule_string: ruleExpression(levels.value.map((level) => level.value)),
    };
  groupEdit.value = null;
}
function openImport(target: 'custom' | 'groups' | 'levels') {
  importing.value = target;
  code.value = '';
  importError.value = '';
}
function importCode() {
  try {
    if (importing.value === 'custom')
      custom.value.push(...importRuleCode(code.value, 'custom').map(wrap));
    if (importing.value === 'groups')
      groups.value.push(...importRuleCode(code.value, 'groups').map(wrap));
    if (importing.value === 'levels' && code.value)
      levels.value = ruleLevels(code.value, true).map(wrap);
    importing.value = null;
  } catch (e) {
    importError.value = errorText(e);
  }
}
async function share(value: unknown, expression = false) {
  try {
    await copyText(expression ? String(value) : JSON.stringify(value));
    toast('规则已复制到剪贴板');
  } catch {
    toast('规则复制失败，请检查浏览器剪贴板权限', true);
  }
}
onMounted(load);
</script>
<template>
  <div class="rule-settings">
    <VAlert v-if="loadError" type="error" variant="tonal"
      >{{ loadError }} <VBtn variant="text" @click="load">重试</VBtn></VAlert
    >
    <p v-if="loading" role="status">加载中…</p>
    <template v-else-if="!loadError">
      <VCard
        v-for="section in ['custom', 'groups'] as const"
        :key="section"
        class="rule-section"
        variant="flat"
      >
        <VCardTitle>{{ section === 'custom' ? '自定义规则' : '优先级规则组' }}</VCardTitle>
        <VCardText>
          <Draggable
            v-if="section === 'custom'"
            v-model="custom"
            item-key="key"
            handle=".rule-drag"
            :animation="150"
            class="rule-grid custom-grid"
          >
            <template #item="{ element }"
              ><VCard
                variant="tonal"
                class="rule-summary"
                role="button"
                tabindex="0"
                :aria-label="'编辑自定义规则 ' + element.value.name"
                @click="editCustom(element)"
                @keydown.enter.self="editCustom(element)"
                @keydown.space.prevent.self="editCustom(element)"
              >
                <div class="rule-card-actions">
                  <VBtn
                    class="rule-drag"
                    icon
                    variant="text"
                    size="small"
                    aria-label="拖动规则"
                    @click.stop
                    ><AppIcon name="grip" /></VBtn
                  ><VBtn
                    icon
                    variant="text"
                    size="small"
                    :aria-label="'移除 ' + element.value.name"
                    @click.stop="custom = custom.filter((item) => item.key !== element.key)"
                    ><AppIcon name="close"
                  /></VBtn>
                </div>
                <VCardText
                  ><h3>{{ element.value.name }}</h3>
                  <span>{{ element.value.id }}</span
                  ><img :src="filterImage" alt=""
                /></VCardText> </VCard
            ></template>
          </Draggable>
          <Draggable
            v-else
            v-model="groups"
            item-key="key"
            handle=".rule-drag"
            :animation="150"
            class="rule-grid groups-grid"
          >
            <template #item="{ element }"
              ><VCard
                variant="tonal"
                class="rule-summary"
                role="button"
                tabindex="0"
                :aria-label="'编辑规则组 ' + element.value.name"
                @click="editGroup(element)"
                @keydown.enter.self="editGroup(element)"
                @keydown.space.prevent.self="editGroup(element)"
              >
                <div class="rule-card-actions">
                  <VBtn
                    class="rule-drag"
                    icon
                    variant="text"
                    size="small"
                    aria-label="拖动规则组"
                    @click.stop
                    ><AppIcon name="grip" /></VBtn
                  ><VBtn
                    icon
                    variant="text"
                    size="small"
                    :aria-label="'移除 ' + element.value.name"
                    @click.stop="groups = groups.filter((item) => item.key !== element.key)"
                    ><AppIcon name="close"
                  /></VBtn>
                </div>
                <VCardText
                  ><h3>{{ element.value.name }}</h3>
                  <span>{{ element.value.category || element.value.media_type || '全部' }}</span
                  ><img :src="groupImage" alt=""
                /></VCardText> </VCard
            ></template>
          </Draggable>
          <VAlert v-if="errors[section]" class="mt-4" type="error" variant="tonal" role="alert">{{
            errors[section]
          }}</VAlert>
        </VCardText>
        <VCardActions class="rule-section-actions">
          <VBtn
            color="primary"
            variant="elevated"
            :loading="saving === section"
            :disabled="!!saving"
            @click="save(section)"
            >保存</VBtn
          >
          <div class="rule-button-group">
            <VBtn
              color="success"
              variant="tonal"
              :disabled="!!saving"
              @click="section === 'custom' ? addCustom() : addGroup()"
              ><AppIcon name="plus" />添加</VBtn
            ><VBtn color="primary" variant="tonal" :disabled="!!saving" @click="openImport(section)"
              >导入</VBtn
            ><VBtn
              color="info"
              variant="tonal"
              @click="share((section === 'custom' ? custom : groups).map((item) => item.value))"
              >分享</VBtn
            ><VBtn
              color="error"
              variant="tonal"
              :disabled="!!saving"
              @click="section === 'custom' ? (custom = []) : (groups = [])"
              >清空</VBtn
            >
          </div>
        </VCardActions>
      </VCard>
      <VCard class="rule-section" variant="flat">
        <VCardTitle>下载规则</VCardTitle>
        <VCardText
          ><VRow
            ><VCol cols="12" md="6"
              ><VSelect
                v-model="priority"
                :items="priorityOptions"
                label="当前使用下载优先规则"
                open-text="选择下载优先规则"
                close-text="收起下载优先规则"
                hint="排在前面的优先级越高，未选择的项不纳入排序"
                persistent-hint
                multiple
                chips
                clearable
                variant="outlined"
                @update:model-value="priority ||= []" /></VCol></VRow
          ><VAlert v-if="errors.priority" class="mt-4" type="error" variant="tonal">{{
            errors.priority
          }}</VAlert></VCardText
        >
        <VCardActions class="rule-section-actions"
          ><VBtn
            color="primary"
            variant="elevated"
            :loading="saving === 'priority'"
            :disabled="!!saving"
            @click="save('priority')"
            >保存</VBtn
          ></VCardActions
        >
      </VCard>
    </template>
    <VDialog
      :model-value="!!customEdit"
      max-width="40rem"
      :fullscreen="!display.mdAndUp.value"
      scrollable
      @update:model-value="!$event && (customEdit = null)"
    >
      <VCard v-if="customEdit" class="rule-dialog">
        <VCardTitle class="rule-dialog-heading"
          ><AppIcon name="filter" />{{ customEdit.original }} - 配置<VBtn
            icon
            variant="text"
            aria-label="关闭规则编辑"
            @click="customEdit = null"
            ><AppIcon name="close" /></VBtn></VCardTitle
        ><VDivider />
        <VCardText
          ><VRow
            ><VCol
              v-for="field in customFields"
              :key="field.key"
              :cols="field.cols === 6 && ['id', 'name'].includes(field.key) ? 12 : field.cols"
              :md="field.cols"
              ><VTextField
                v-model="customEdit.value[field.key]"
                :label="field.label"
                :placeholder="field.placeholder"
                :hint="field.hint"
                persistent-hint
                variant="outlined"
                @update:model-value="
                  field.key === 'id' &&
                  (customEdit.value.id = String($event || '').replace(/[^a-zA-Z0-9]/g, ''))
                " /></VCol></VRow
          ><VAlert v-if="editorError" type="error" variant="tonal">{{
            editorError
          }}</VAlert></VCardText
        >
        <VCardActions class="rule-dialog-actions"
          ><VBtn color="primary" variant="elevated" @click="confirmCustom">确定</VBtn></VCardActions
        >
      </VCard>
    </VDialog>
    <VDialog
      :model-value="!!groupEdit"
      max-width="80rem"
      :fullscreen="!display.mdAndUp.value"
      scrollable
      @update:model-value="!$event && (groupEdit = null)"
    >
      <VCard v-if="groupEdit" class="rule-dialog">
        <VCardTitle class="rule-dialog-heading"
          ><AppIcon name="filter" />{{ groupEdit.original }} - 过滤规则<VBtn
            icon
            variant="text"
            aria-label="关闭规则组编辑"
            @click="groupEdit = null"
            ><AppIcon name="close" /></VBtn></VCardTitle
        ><VDivider />
        <VCardText
          ><VRow
            ><VCol cols="12" md="6"
              ><VTextField
                v-model="groupEdit.value.name"
                label="规则组名称"
                variant="outlined" /></VCol
            ><VCol cols="6" md="3"
              ><VSelect
                v-model="groupEdit.value.media_type"
                label="媒体类型"
                open-text="选择媒体类型"
                close-text="收起媒体类型"
                :items="[
                  { title: '全部', value: '' },
                  { title: '电影', value: '电影' },
                  { title: '电视剧', value: '电视剧' },
                ]"
                variant="outlined" /></VCol
            ><VCol cols="6" md="3"
              ><VSelect
                v-model="groupEdit.value.category"
                label="媒体类别"
                open-text="选择媒体类别"
                close-text="收起媒体类别"
                :items="categoryOptions"
                variant="outlined" /></VCol
          ></VRow>
          <Draggable
            v-model="levels"
            item-key="key"
            handle=".rule-drag"
            :animation="150"
            class="rule-grid levels-grid"
            ><template #item="{ element, index }"
              ><VCard variant="tonal" class="rule-level"
                ><VCardTitle>优先级 {{ index + 1 }}</VCardTitle>
                <div class="rule-card-actions">
                  <VBtn
                    class="rule-drag"
                    icon
                    variant="text"
                    size="small"
                    aria-label="拖动优先级"
                    @click.stop
                    ><AppIcon name="grip" /></VBtn
                  ><VBtn
                    icon
                    variant="text"
                    size="small"
                    :aria-label="'移除优先级 ' + (index + 1)"
                    @click="levels = levels.filter((item) => item.key !== element.key)"
                    ><AppIcon name="close"
                  /></VBtn>
                </div>
                <VCardText
                  ><VAutocomplete
                    v-model="element.value"
                    :items="options"
                    label="规则"
                    variant="underlined"
                    chips
                    multiple
                    clearable
                    @update:model-value="element.value ||= []" /></VCardText></VCard></template
          ></Draggable>
          <VAlert v-if="editorError" class="mt-4" type="error" variant="tonal">{{
            editorError
          }}</VAlert> </VCardText
        ><VCardActions class="rule-dialog-actions group-footer"
          ><VBtn
            color="primary"
            variant="tonal"
            aria-label="添加规则"
            @click="levels.push(wrap([]))"
            ><AppIcon name="plus" /></VBtn
          ><VBtn color="success" variant="tonal" @click="openImport('levels')">导入规则</VBtn
          ><VBtn
            color="info"
            variant="tonal"
            @click="share(ruleExpression(levels.map((item) => item.value)), true)"
            >分享规则</VBtn
          ><VBtn class="group-save" color="primary" variant="elevated" @click="confirmGroup"
            >保存规则</VBtn
          ></VCardActions
        >
      </VCard>
    </VDialog>
    <VDialog
      :model-value="!!importing"
      max-width="40rem"
      scrollable
      @update:model-value="!$event && (importing = null)"
    >
      <VCard class="rule-dialog"
        ><VCardTitle class="rule-dialog-heading"
          >{{
            importing === 'custom'
              ? '导入自定义规则'
              : importing === 'groups'
                ? '导入规则组'
                : '导入规则'
          }}<VBtn icon variant="text" aria-label="关闭导入" @click="importing = null"
            ><AppIcon name="close" /></VBtn></VCardTitle
        ><VDivider /><VCardText
          ><VTextarea
            v-model="code"
            label="代码"
            rows="10"
            variant="outlined"
            spellcheck="false"
          /><VAlert v-if="importError" type="error" variant="tonal">{{
            importError
          }}</VAlert></VCardText
        ><VCardActions class="rule-dialog-actions"
          ><VBtn color="primary" variant="elevated" @click="importCode">导入</VBtn></VCardActions
        ></VCard
      >
    </VDialog>
  </div>
</template>
<style scoped>
.rule-settings {
  display: grid;
  gap: 24px;
}
.rule-settings :deep(.v-label),
.rule-dialog :deep(.v-label) {
  flex-direction: row;
  gap: 0;
}
.rule-settings :deep(.v-field input),
.rule-dialog :deep(.v-field input),
.rule-dialog :deep(.v-field textarea) {
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
  color: inherit;
  font: inherit;
  line-height: inherit;
  min-width: 0;
}
.rule-settings :deep(.v-field__input > input),
.rule-dialog :deep(.v-field__input > input) {
  padding: 0;
}
.rule-dialog :deep(.v-field textarea) {
  resize: none;
}
.rule-section {
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 8px;
  box-shadow: 0 2px 8px #2f2b3d12;
}
.rule-section > .v-card-title {
  padding: 20px 24px 8px;
  font-size: 1.125rem;
  font-weight: 500;
}
.rule-section > .v-card-text {
  padding: 16px 24px;
}
.rule-grid {
  display: grid;
  gap: 16px;
}
.custom-grid {
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 14rem), 1fr));
}
.groups-grid {
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 16rem), 1fr));
}
.levels-grid {
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 15rem), 1fr));
  margin-top: 8px;
}
.rule-summary {
  min-height: 120px;
  cursor: pointer;
  border-radius: 8px;
  background: linear-gradient(135deg, #e7e4f94d, #efedf62b 46%, #fff0 76%), #f5f4f8;
  box-shadow: none;
}
.rule-summary .v-card-text {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  height: 120px;
  padding: 16px 24px;
  position: relative;
}
.rule-summary h3 {
  font-size: 1.125rem;
  line-height: 1.5;
  font-weight: 500;
  margin: 0;
  padding-right: 56px;
  overflow-wrap: anywhere;
}
.rule-summary span {
  opacity: 0.7;
  padding-right: 64px;
  overflow-wrap: anywhere;
}
.rule-summary img {
  position: absolute;
  bottom: 12px;
  right: 16px;
  width: 56px;
  height: 56px;
  object-fit: contain;
}
.rule-card-actions {
  position: absolute;
  z-index: 1;
  top: 4px;
  right: 4px;
  display: flex;
  gap: 0;
}
.rule-drag {
  cursor: move;
  touch-action: none;
}
.rule-card-actions .v-btn {
  width: 32px;
  height: 32px;
  min-width: 32px;
}
.rule-level {
  border-radius: 8px;
  box-shadow: none;
  min-width: 0;
}
.rule-level .v-card-title {
  font-size: 1.125rem;
  padding-right: 70px;
}
.rule-level .v-card-text {
  padding-top: 8px;
}
.rule-section-actions {
  padding: 8px 24px 24px;
  flex-wrap: wrap;
  gap: 12px;
}
.rule-button-group {
  display: flex;
  border-radius: 6px;
  overflow: hidden;
  flex-wrap: wrap;
}
.rule-button-group .v-btn {
  border-radius: 0;
  margin: 0;
  min-width: 64px;
}
.rule-button-group .app-icon {
  margin-right: 4px;
  width: 18px;
}
.rule-dialog-heading {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 24px;
  white-space: normal;
  font-size: 1.25rem;
}
.rule-dialog-heading > .v-btn {
  margin-left: auto;
  flex-shrink: 0;
}
.rule-dialog .v-card-text {
  padding: 24px;
}
.rule-dialog-actions {
  padding: 12px 24px 20px;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
}
.group-footer {
  justify-content: flex-start;
}
.group-save {
  margin-left: auto;
}
@media (max-width: 599px) {
  .rule-section > .v-card-title {
    padding: 16px 16px 8px;
  }
  .rule-section > .v-card-text {
    padding: 12px 16px;
  }
  .rule-section-actions {
    padding: 8px 16px 16px;
  }
  .rule-dialog .v-card-text {
    padding: 16px;
  }
  .rule-dialog-heading {
    padding: 12px 16px;
  }
  .rule-dialog-actions {
    padding: 12px 16px;
    gap: 4px;
  }
  .group-footer .v-btn {
    min-width: 40px;
    padding-inline: 10px;
  }
}
</style>

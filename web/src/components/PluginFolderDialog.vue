<script setup lang="ts">
import { ref } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import {
  VCard,
  VCardTitle,
  VCardText,
  VCardActions,
  VCardSubtitle,
} from 'vuetify/components/VCard';
import { VTextField } from 'vuetify/components/VTextField';
import { VSwitch } from 'vuetify/components/VSwitch';
import { VRow, VCol } from 'vuetify/components/VGrid';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VDivider } from 'vuetify/components/VDivider';
import { VAlert } from 'vuetify/components/VAlert';
import {
  folderIcons,
  folderColors,
  folderGradients,
  type FolderConfig,
} from '../features/plugin-organization';
const props = defineProps<{
  mode: 'create' | 'rename' | 'appearance';
  name: string;
  config?: FolderConfig;
  busy: boolean;
  error: string;
}>();
const emit = defineEmits<{ close: []; save: [value: { name: string; config: FolderConfig }] }>();
const display = useDisplay();
const name = ref(props.name);
const settings = ref<FolderConfig>({
  ...props.config,
  background: props.config?.background || '',
  icon: props.config?.icon || folderIcons[0],
  color: props.config?.color || folderColors[0],
  gradient: props.config?.gradient || folderGradients[0],
  showIcon: props.config?.showIcon !== undefined ? props.config.showIcon : true,
});
function save() {
  if (!props.busy) emit('save', { name: name.value, config: settings.value });
}
</script>
<template>
  <VDialog
    :model-value="true"
    :max-width="mode === 'appearance' ? 600 : 400"
    :fullscreen="mode === 'appearance' && !display.mdAndUp.value"
    scrollable
    :persistent="busy"
    content-class="plugin-dialog-surface"
    @update:model-value="!busy && emit('close')"
  >
    <VCard
      ><VCardTitle class="plugin-dialog-title"
        >{{
          mode === 'create' ? '新建文件夹' : mode === 'rename' ? '重命名文件夹' : '文件夹外观设置'
        }}<VBtn
          variant="text"
          icon
          :disabled="busy"
          color="secondary"
          aria-label="关闭文件夹设置"
          @click="emit('close')"
          ><VIcon icon="mdi-close" /></VBtn></VCardTitle
      ><VDivider />
      <VCardText class="plugin-dialog-content"
        ><VAlert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</VAlert>
        <VTextField
          v-if="mode !== 'appearance'"
          v-model="name"
          label="文件夹名称"
          variant="outlined"
          autofocus
          :disabled="busy"
          @keyup.enter="save"
        />
        <VRow v-else>
          <VCol cols="12"
            ><VSwitch
              v-model="settings.showIcon"
              label="显示文件夹图标"
              color="primary"
              hide-details
              :disabled="busy"
          /></VCol>
          <VCol v-if="settings.showIcon" cols="12" md="6"
            ><VCardSubtitle class="pa-0 mb-2">图标</VCardSubtitle>
            <div class="icon-grid">
              <VBtn
                v-for="icon in folderIcons"
                :key="icon"
                icon
                :variant="settings.icon === icon ? 'tonal' : 'text'"
                :color="settings.icon === icon ? 'primary' : 'default'"
                size="large"
                :disabled="busy"
                :aria-label="icon"
                :aria-pressed="settings.icon === icon"
                @click="settings.icon = icon"
                ><VIcon :icon="icon" size="24"
              /></VBtn></div
          ></VCol>
          <VCol v-if="settings.showIcon" cols="12" md="6"
            ><VCardSubtitle class="pa-0 mb-2">图标颜色</VCardSubtitle>
            <div class="color-grid">
              <VBtn
                v-for="color in folderColors"
                :key="color"
                :variant="settings.color === color ? 'tonal' : 'text'"
                :color="color"
                size="large"
                class="color-btn"
                :style="{ backgroundColor: color }"
                :disabled="busy"
                :aria-label="'颜色' + color"
                :aria-pressed="settings.color === color"
                @click="settings.color = color"
                ><VIcon v-if="settings.color === color" icon="mdi-check" color="white"
              /></VBtn></div
          ></VCol>
          <VCol cols="12"
            ><VCardSubtitle class="pa-0 mb-2">背景渐变</VCardSubtitle>
            <div class="gradient-grid">
              <VBtn
                v-for="(gradient, index) in folderGradients"
                :key="gradient"
                class="gradient-btn"
                :style="{ background: gradient }"
                size="large"
                :disabled="busy"
                :aria-label="'渐变' + (index + 1)"
                :aria-pressed="settings.gradient === gradient"
                @click="settings.gradient = gradient"
                ><VIcon v-if="settings.gradient === gradient" icon="mdi-check" color="white"
              /></VBtn></div
          ></VCol>
          <VCol cols="12"
            ><VTextField
              v-model="settings.background"
              label="自定义背景图片URL"
              placeholder="https://example.com/image.jpg"
              variant="outlined"
              hint="设置图片后将优先显示背景图片"
              persistent-hint
              prepend-inner-icon="mdi-image"
              :disabled="busy"
          /></VCol>
        </VRow> </VCardText
      ><VCardActions class="folder-dialog-actions"
        ><VBtn
          color="primary"
          variant="flat"
          :prepend-icon="
            mode === 'create'
              ? 'mdi-folder-plus'
              : mode === 'rename'
                ? 'mdi-check'
                : 'mdi-content-save'
          "
          :loading="busy"
          :disabled="busy"
          @click="save"
          >{{ mode === 'create' ? '创建' : mode === 'rename' ? '确认' : '保存' }}</VBtn
        ></VCardActions
      >
    </VCard>
  </VDialog>
</template>
<style scoped>
.icon-grid,
.color-grid,
.gradient-grid {
  display: grid;
  gap: 8px;
  grid-template-columns: repeat(auto-fill, minmax(60px, 1fr));
}
.icon-grid {
  max-height: 200px;
  overflow-y: auto;
}
.color-btn,
.gradient-btn {
  min-width: 0 !important;
}
.folder-dialog-actions {
  justify-content: flex-end;
  padding: 16px 24px;
}
</style>

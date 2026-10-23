<script setup lang="ts">
import { computed } from 'vue';
import { VCard } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VMenu } from 'vuetify/components/VMenu';
import { VList, VListItem } from 'vuetify/components/VList';
import {
  folderIcons,
  folderColors,
  folderGradients,
  safeFolderBackground,
  type FolderConfig,
} from '../features/plugin-organization';
const props = defineProps<{
  name: string;
  count: number;
  config: FolderConfig;
  sortable: boolean;
}>();
const emit = defineEmits<{ open: []; rename: []; appearance: []; delete: [] }>();
const image = computed(() => safeFolderBackground(props.config.background));
const background = computed(() =>
  image.value
    ? {
        backgroundImage: `linear-gradient(rgba(0,0,0,.6),rgba(0,0,0,.6)),url(${JSON.stringify(image.value)})`,
      }
    : {
        background: folderGradients.includes(props.config.gradient || '')
          ? props.config.gradient
          : folderGradients[0],
      },
);
</script>
<template>
  <VCard
    class="plugin-folder-card"
    :class="{ sortable }"
    :style="background"
    @click="!sortable && emit('open')"
  >
    <div class="folder-body" :class="{ 'no-icon': config.showIcon === false }">
      <VIcon
        v-if="config.showIcon !== false"
        class="folder-icon"
        :icon="folderIcons.includes(config.icon || '') ? config.icon : 'mdi-folder'"
        :color="folderColors.includes(config.color || '') ? config.color : folderColors[0]"
      />
      <div class="folder-info">
        <h3>{{ name }}</h3>
        <p>{{ count }} 个插件</p>
      </div>
    </div>
    <VMenu
      v-if="!sortable"
      location="top end"
      :transition="false"
      content-class="plugin-dialog-surface"
      ><template #activator="{ props: menuProps }"
        ><VBtn
          v-bind="menuProps"
          class="folder-menu"
          variant="text"
          icon
          size="40"
          :aria-label="name + '文件夹操作'"
          @click.stop
          ><VIcon icon="mdi-dots-vertical" size="20" /></VBtn></template
      ><VList
        ><VListItem
          title="设置外观"
          prepend-icon="mdi-palette"
          @click="emit('appearance')" /><VListItem
          title="重命名"
          prepend-icon="mdi-pencil"
          @click="emit('rename')" /><VListItem
          title="删除文件夹"
          prepend-icon="mdi-delete"
          base-color="error"
          @click="emit('delete')" /></VList
    ></VMenu>
  </VCard>
</template>
<style scoped>
.plugin-folder-card {
  position: relative;
  overflow: hidden;
  cursor: pointer;
  min-height: 136px;
  border: 0;
  border-radius: 12px;
  height: 100%;
  background-size: cover;
  background-position: top;
  transition: transform 0.3s cubic-bezier(0.4, 0, 0.2, 1);
}
.plugin-folder-card:not(.sortable):hover {
  transform: translateY(-4px);
}
.sortable {
  cursor: move;
}
.folder-body {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 16px 24px;
  height: 100%;
  min-height: 136px;
}
.folder-body.no-icon {
  align-items: flex-start;
  padding: 32px;
}
.folder-icon {
  font-size: 72px;
  flex-shrink: 0;
}
.folder-info {
  min-width: 0;
  flex: 1;
  text-align: start;
}
.folder-info h3 {
  color: white;
  font-size: 1.1rem;
  font-weight: 600;
  line-height: 1.3;
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-shadow: 0 2px 4px #0008;
}
.folder-info p {
  color: white;
  font-size: 0.85rem;
  margin: 2px 0 0;
  opacity: 0.9;
  text-shadow: 0 1px 2px #0008;
}
.no-icon h3 {
  font-size: 1.3rem;
  font-weight: 700;
}
.folder-menu {
  position: absolute;
  inset-block-start: 0;
  inset-inline-end: 0;
  color: white;
}
@media (max-width: 640px) {
  .folder-body {
    gap: 12px;
    padding: 12px 16px;
  }
  .folder-icon {
    font-size: 56px;
  }
  .folder-info h3 {
    font-size: 1rem;
  }
  .folder-info p {
    font-size: 0.8rem;
  }
}
</style>

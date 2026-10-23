<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { VCard, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VMenu } from 'vuetify/components/VMenu';
import { VList, VListItem } from 'vuetify/components/VList';
import { VIcon } from 'vuetify/components/VIcon';
import type { PluginCard } from '../features/plugin-forms';
import { safePluginIcon } from '../features/plugin-clone';
import AppIcon from './AppIcon.vue';
const props = defineProps<{
  plugin: PluginCard;
  busy?: boolean;
  sortable?: boolean;
  inFolder?: boolean;
}>();
const emit = defineEmits<{ action: [action: string, plugin: PluginCard] }>();
const icon = computed(() => safePluginIcon(props.plugin.icon));
const iconFailed = ref(false);
watch(icon, () => {
  iconFailed.value = false;
});
</script>
<template>
  <div class="plugin-item-wrapper">
    <VCard
      class="plugin-card"
      :class="{ sortable }"
      @click="!sortable && emit('action', 'open', plugin)"
    >
      <div class="plugin-card-cover">
        <VCardTitle class="plugin-card-title"
          ><span class="plugin-state-dot" :class="{ active: plugin.state }"></span>{{ plugin.name
          }}<span class="plugin-version"
            >v{{ plugin.display_version ?? plugin.version }}</span
          ></VCardTitle
        >
        <div class="plugin-card-description">
          <span>{{ plugin.description }}</span>
          <div class="plugin-card-icon">
            <img
              v-if="icon && !iconFailed"
              :key="icon"
              :src="icon"
              alt=""
              referrerpolicy="no-referrer"
              @error="iconFailed = true"
            />
            <AppIcon v-else name="plugin" />
          </div>
        </div>
      </div>
      <VCardText class="plugin-card-footer"
        ><a :href="plugin.author_url" target="_blank" rel="noopener noreferrer" @click.stop>{{
          plugin.author
        }}</a>
        <VBtn
          v-if="!plugin.installed"
          size="small"
          variant="text"
          :disabled="busy"
          @click.stop="emit('action', 'install', plugin)"
          >安装</VBtn
        >
        <VMenu v-else-if="!sortable" location="bottom end" :transition="false">
          <template #activator="{ props: menuProps }"
            ><VBtn
              v-bind="menuProps"
              variant="text"
              icon
              size="small"
              :aria-label="plugin.name + '操作'"
              :disabled="busy"
              @click.stop
              ><AppIcon name="more" /></VBtn
          ></template>
          <VList class="plugin-menu"
            ><VListItem
              v-if="plugin.has_page"
              title="详情"
              @click="emit('action', 'detail', plugin)" /><VListItem
              title="设置"
              @click="emit('action', 'settings', plugin)" /><VListItem
              title="分身"
              :disabled="busy"
              @click="!busy && emit('action', 'clone', plugin)" /><VListItem
              v-if="plugin.has_update && plugin.update"
              :title="'更新至 v' + plugin.update.target_version"
              @click="emit('action', 'update', plugin)" /><VListItem
              v-if="plugin.update_blocked_reason"
              disabled
              :title="'更新暂不可用：' + plugin.update_blocked_reason" /><VListItem
              title="重置"
              @click="emit('action', 'reset', plugin)" /><VListItem
              title="卸载"
              @click="emit('action', 'uninstall', plugin)" /><VListItem
              title="执行记录"
              @click="emit('action', 'runs', plugin)" /><VListItem
              title="查看日志"
              @click="emit('action', 'logs', plugin)"
          /></VList>
        </VMenu>
      </VCardText>
    </VCard>
    <VBtn
      v-if="inFolder && !sortable"
      class="remove-from-folder-btn"
      icon
      variant="text"
      color="warning"
      size="small"
      :aria-label="'将' + plugin.name + '移出文件夹'"
      @click.stop="emit('action', 'remove-folder', plugin)"
      ><VIcon icon="mdi-folder-remove"
    /></VBtn>
  </div>
</template>
<style scoped>
.plugin-item-wrapper {
  position: relative;
  height: 100%;
}
.plugin-card {
  cursor: pointer;
  border: 1px solid var(--surface-border);
  overflow: hidden;
  height: 100%;
}
.plugin-card.sortable {
  cursor: move;
}
.plugin-card-cover {
  background: linear-gradient(rgba(0, 0, 0, 0.6), rgba(0, 0, 0, 0.5)), #28a9e1;
  min-height: 108px;
  color: #fff;
}
.plugin-card-title {
  padding: 12px 16px 4px;
  font-size: 18px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.plugin-state-dot {
  height: 8px;
  width: 8px;
  background: #8a8d93;
  border-radius: 50%;
  flex: none;
}
.plugin-state-dot.active {
  background: #56ca00;
}
.plugin-version {
  font-size: 13px;
  opacity: 0.8;
}
.plugin-card-description {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 16px 12px;
  gap: 12px;
  font-size: 14px;
}
.plugin-card-description > span {
  line-height: 1.5;
  min-height: 42px;
}
.plugin-card-icon {
  width: 48px;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: #ffffff21;
  flex: none;
}
.plugin-card-icon .app-icon {
  width: 28px;
  height: 28px;
}
.plugin-card-icon img {
  width: 40px;
  height: 40px;
  object-fit: contain;
}
.plugin-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 8px 2px 16px !important;
  height: 40px;
}
.plugin-card-footer a {
  font-size: 14px;
}
.remove-from-folder-btn {
  position: absolute;
  z-index: 3;
  inset-block-start: 4px;
  inset-inline-end: 4px;
}
</style>

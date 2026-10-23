<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { VCard, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VMenu } from 'vuetify/components/VMenu';
import { VList, VListItem } from 'vuetify/components/VList';
import { VIcon } from 'vuetify/components/VIcon';
import type { PluginCard } from '../features/plugin-forms';
import iconColors from '../features/plugin-icon-colors.json';
import { safePluginIcon } from '../features/plugin-clone';

const props = defineProps<{
  plugin: PluginCard;
  busy?: boolean;
  sortable?: boolean;
  inFolder?: boolean;
}>();
const emit = defineEmits<{ action: [action: string, plugin: PluginCard] }>();
const iconFailed = ref(false);
const sourceIcon = computed(() => props.plugin.icon || '');
const sourceIconName = computed(() => sourceIcon.value.split('/').at(-1) || '');
const displayVersion = computed(() => props.plugin.display_version ?? props.plugin.version);
const iconName = computed(() => {
  const name = sourceIconName.value;
  return !iconFailed.value &&
    /^[A-Za-z0-9_.-]+\.(?:png|jpe?g)$/i.test(name) &&
    Object.hasOwn(iconColors, name)
    ? name
    : 'plugin.png';
});
const iconPath = computed(() => {
  if (
    iconFailed.value ||
    Object.hasOwn(iconColors, sourceIconName.value) ||
    !sourceIcon.value.includes('/') ||
    sourceIcon.value.startsWith('../')
  )
    return safePluginIcon(iconName.value);
  return safePluginIcon(sourceIcon.value) || safePluginIcon('plugin.png');
});
const coverStyle = computed(() => {
  // These colors are generated with MoviePilot's ColorThief quality=20 from
  // the bundled original icons, keeping the original cover offline and stable.
  const color = (iconColors as Record<string, string>)[iconName.value] || '#28a9e1';
  return { background: `linear-gradient(rgba(0, 0, 0, .6), rgba(0, 0, 0, .5)), ${color}` };
});
watch(sourceIcon, () => {
  iconFailed.value = false;
});
</script>
<template>
  <div class="plugin-item-wrapper">
    <VCard
      class="plugin-card"
      :class="{ sortable }"
      :ripple="!sortable"
      @click="!sortable && emit('action', 'open', plugin)"
    >
      <div class="plugin-card-cover" :style="coverStyle">
        <div class="plugin-card-title" :title="plugin.name + ' v' + displayVersion">
          <span class="plugin-state-dot" :class="{ active: plugin.state }"></span>
          <span class="plugin-card-name"
            >{{ plugin.name }} <span class="plugin-version">v{{ displayVersion }}</span></span
          >
        </div>
        <div class="plugin-card-description">
          <span :title="plugin.description">{{ plugin.description }}</span>
          <div class="plugin-card-icon">
            <img
              :key="iconPath"
              :src="iconPath"
              alt=""
              referrerpolicy="no-referrer"
              @error="iconFailed = true"
            />
          </div>
        </div>
      </div>
      <VCardText class="plugin-card-footer">
        <div class="plugin-author">
          <VIcon class="author-avatar" icon="mdi-github" size="20" />
          <span v-if="sortable">{{ plugin.author }}</span>
          <a
            v-else
            :href="plugin.author_url"
            target="_blank"
            rel="noopener noreferrer"
            @click.stop
            >{{ plugin.author }}</a
          >
        </div>
        <VBtn
          v-if="!plugin.installed"
          size="small"
          variant="text"
          :disabled="busy || !!plugin.restore_review_blocked_reason"
          :title="plugin.restore_review_blocked_reason || undefined"
          @click.stop="
            emit('action', plugin.restore_review ? 'restore-reviewed' : 'install', plugin)
          "
          >{{
            plugin.restore_review
              ? '恢复为待确认'
              : plugin.restore_review_blocked_reason
                ? '恢复暂不可用'
                : '安装'
          }}</VBtn
        >
        <VMenu
          v-else-if="!sortable"
          location="bottom end"
          :transition="false"
          content-class="plugin-dialog-surface"
        >
          <template #activator="{ props: menuProps }">
            <VBtn
              v-bind="menuProps"
              class="plugin-menu-button"
              variant="text"
              color="default"
              icon
              size="40"
              :aria-label="plugin.name + '操作'"
              :disabled="busy"
              @click.stop
            >
              <VIcon icon="mdi-dots-vertical" size="24" />
            </VBtn>
          </template>
          <VList class="plugin-menu">
            <VListItem
              v-if="plugin.has_page"
              title="详情"
              prepend-icon="mdi-information-outline"
              @click="emit('action', 'detail', plugin)"
            />
            <VListItem
              title="设置"
              prepend-icon="mdi-cog-outline"
              @click="emit('action', 'settings', plugin)"
            />
            <VListItem
              title="分身"
              prepend-icon="mdi-content-copy"
              :disabled="busy"
              @click="!busy && emit('action', 'clone', plugin)"
            />
            <VListItem
              v-if="plugin.has_update && plugin.update"
              :title="'更新至 v' + plugin.update.target_version"
              prepend-icon="mdi-update"
              @click="emit('action', 'update', plugin)"
            />
            <VListItem
              v-if="plugin.update_review_pending"
              title="更新已迁移，打开设置确认执行意图"
              @click="emit('action', 'settings', plugin)"
            />
            <VListItem
              v-if="plugin.update_blocked_reason"
              disabled
              :title="'更新暂不可用：' + plugin.update_blocked_reason"
            />
            <VListItem
              title="重置"
              prepend-icon="mdi-cancel"
              base-color="warning"
              @click="emit('action', 'reset', plugin)"
            />
            <VListItem
              title="卸载"
              prepend-icon="mdi-trash-can-outline"
              base-color="error"
              @click="emit('action', 'uninstall', plugin)"
            />
            <VListItem
              title="执行记录"
              prepend-icon="mdi-history"
              @click="emit('action', 'runs', plugin)"
            />
            <VListItem
              title="查看日志"
              prepend-icon="mdi-file-document-outline"
              @click="emit('action', 'logs', plugin)"
            />
          </VList>
        </VMenu>
      </VCardText>
      <VIcon v-if="plugin.has_update" class="plugin-update" icon="mdi-new-box" color="white" />
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
  min-width: 0;
}
.plugin-card {
  cursor: pointer;
  overflow: hidden;
  height: 100%;
  border: 0;
  border-radius: 12px;
  transition: transform 0.3s;
}
.plugin-card:not(.sortable):hover {
  transform: translateY(-4px);
}
.plugin-card.sortable {
  cursor: move;
}
.plugin-card-cover {
  height: 108px;
  color: #fff;
}
.plugin-card-title {
  padding: 12px 16px 0;
  height: 36px;
  font-size: 16px;
  font-weight: 600;
  line-height: 24px;
  display: flex;
  align-items: center;
  gap: 8px;
  text-shadow: 0 1px 2px #0006;
}
.plugin-card-name {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
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
  font-size: 14px;
  font-weight: 400;
  color: #e5e7eb;
}
.plugin-card-description {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 4px 8px 8px 16px;
  gap: 8px;
  font-size: 14px;
}
.plugin-card-description > span {
  flex: 1;
  min-width: 0;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  overflow: hidden;
  line-height: 20px;
  text-shadow: 0 1px 2px #0006;
}
.plugin-card-icon {
  width: 48px;
  height: 48px;
  margin-top: 2px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  overflow: hidden;
  flex: none;
}
.plugin-card-icon img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.plugin-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px !important;
  height: 40px;
  min-height: 40px;
  gap: 8px;
}
.plugin-author {
  display: flex;
  align-items: center;
  min-width: 0;
  padding-right: 32px;
  font-size: 14px;
}
.plugin-author a,
.plugin-author > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.author-avatar {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  margin-right: 8px;
  flex: none;
  object-fit: cover;
}
.plugin-menu-button {
  position: absolute;
  right: 0;
  bottom: 0;
}
.plugin-update {
  position: absolute;
  right: 8px;
  top: 0;
}
.remove-from-folder-btn {
  position: absolute;
  z-index: 3;
  inset-block-start: 4px;
  inset-inline-end: 4px;
}
</style>

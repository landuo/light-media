<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { VCard, VCardText } from "vuetify/components/VCard";
import { VBtn } from "vuetify/components/VBtn";
import { VMenu } from "vuetify/components/VMenu";
import { VList, VListItem } from "vuetify/components/VList";
import { VIcon } from "vuetify/components/VIcon";
import { VChip } from "vuetify/components/VChip";
import { validPluginLink, type PluginCard } from "../features/plugin-forms";
import iconColors from "../features/plugin-icon-colors.json";
import { safePluginIcon } from "../features/plugin-clone";

const props = defineProps<{
  plugin: PluginCard;
  busy?: boolean;
  sortable?: boolean;
  inFolder?: boolean;
  count?: number;
}>();
const emit = defineEmits<{ action: [action: string, plugin: PluginCard] }>();
const iconFailed = ref(false);
const sourceIcon = computed(() => props.plugin.icon || "");
const sourceIconName = computed(() => sourceIcon.value.split("/").at(-1) || "");
const displayVersion = computed(
  () => props.plugin.display_version ?? props.plugin.version,
);
const labels = computed(() => (props.plugin.plugin_label || '').split(',').map(label => label.trim()).filter(Boolean));
const downloadCount = computed(() => props.count && props.count >= 1000 ? (props.count / 1000).toFixed(1) + 'k' : props.count?.toLocaleString() || '0');
const iconName = computed(() => {
  const name = sourceIconName.value;
  return !iconFailed.value &&
    /^[A-Za-z0-9_.-]+\.(?:png|jpe?g)$/i.test(name) &&
    Object.hasOwn(iconColors, name)
    ? name
    : "plugin.png";
});
const iconPath = computed(() => {
  if (
    iconFailed.value ||
    Object.hasOwn(iconColors, sourceIconName.value) ||
    !sourceIcon.value.includes("/") ||
    sourceIcon.value.startsWith("../")
  )
    return safePluginIcon(iconName.value);
  return safePluginIcon(sourceIcon.value) || safePluginIcon("plugin.png");
});
const coverStyle = computed(() => {
  // These colors are generated with MoviePilot's ColorThief quality=20 from
  // the bundled original icons, keeping the original cover offline and stable.
  const color =
    (iconColors as Record<string, string>)[iconName.value] || "#28a9e1";
  return {
    background: `linear-gradient(rgba(0, 0, 0, .6), rgba(0, 0, 0, .5)), ${color}`,
  };
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
      :ripple="!sortable && plugin.available !== false"
      @click="
        !sortable &&
        plugin.available !== false &&
        emit('action', 'open', plugin)
      "
    >
      <div class="plugin-card-cover" :style="coverStyle">
        <div
          class="plugin-card-title"
          :title="plugin.name + ' v' + displayVersion"
        >
          <span
            class="plugin-state-dot"
            :class="{ active: plugin.state }"
          ></span>
          <span class="plugin-card-name"
            >{{ plugin.name }}
            <span class="plugin-version">v{{ displayVersion }}</span></span
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
        <div v-if="labels.length" class="plugin-card-labels"><VChip v-for="label in labels" :key="label" size="x-small" variant="tonal" color="info" class="mr-1">{{ label }}</VChip></div>
      </div>
      <p v-if="plugin.available === false" role="status" class="pa-3 mb-0">
        {{ plugin.unavailable_reason || "插件暂不可用" }}
      </p>
      <p v-else-if="plugin.has_update && plugin.update_blocked_reason" role="status" class="pa-3 mb-0">{{ plugin.update_blocked_reason }}</p>
      <VCardText class="plugin-card-footer">
        <div class="plugin-author">
          <VIcon class="author-avatar" icon="mdi-github" size="20" />
          <span v-if="sortable || !validPluginLink(plugin.author_url)">{{ plugin.author }}</span>
          <a
            v-else
            :href="plugin.author_url"
            target="_blank"
            rel="noopener noreferrer"
            @click.stop
            >{{ plugin.author }}</a
          >
        </div>
        <span v-if="count" class="plugin-download-count"><VIcon size="small" icon="mdi-download" />{{ downloadCount }}</span>
        <VBtn
          v-if="!plugin.installed"
          size="small"
          variant="text"
          :disabled="busy || plugin.available === false"
          :title="
            (plugin.available === false && plugin.unavailable_reason) ||
            undefined
          "
          @click.stop="emit('action', 'install', plugin)"
          >安装</VBtn
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
              :disabled="plugin.available === false"
              prepend-icon="mdi-cog-outline"
              @click="emit('action', 'settings', plugin)"
            />
            <VListItem
              title="分身"
              prepend-icon="mdi-content-copy"
              :disabled="busy || plugin.available === false"
              @click="
                !busy &&
                plugin.available !== false &&
                emit('action', 'clone', plugin)
              "
            />
            <VListItem
              v-if="
                plugin.has_update &&
                plugin.repo_url &&
                plugin.repository_version
              "
              :title="'更新至 v' + plugin.repository_version"
              prepend-icon="mdi-update"
              :disabled="plugin.available === false || !!plugin.update_blocked_reason"
              @click="!plugin.update_blocked_reason && emit('action', 'repository-update', plugin)"
            />
            <VListItem
              title="重置"
              :disabled="plugin.available === false"
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
              title="查看日志"
              prepend-icon="mdi-file-document-outline"
              @click="emit('action', 'logs', plugin)"
            />
            <VListItem
              title="版本历史"
              prepend-icon="mdi-history"
              @click="emit('action', 'history', plugin)"
            />
            <VListItem title="项目主页" prepend-icon="mdi-home-outline" @click="emit('action', 'project', plugin)" />
          </VList>
        </VMenu>
      </VCardText>
      <VIcon
        v-if="plugin.has_update"
        class="plugin-update"
        icon="mdi-new-box"
        color="white"
      />
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
.plugin-card-cover:has(.plugin-card-labels) { height: 128px; }
.plugin-card-cover:has(.plugin-card-labels) .plugin-card-description > span { -webkit-line-clamp: 2; line-clamp: 2; }
.plugin-card-labels { padding: 0 16px 8px; white-space: nowrap; overflow: hidden; }
.plugin-download-count { display: inline-flex; align-items: center; flex-shrink: 0; gap: 4px; font-size: 12px; }
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

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useDisplay } from 'vuetify';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard } from 'vuetify/components/VCard';
import { VTextField } from 'vuetify/components/VTextField';
import { VList, VListItem, VListItemTitle, VListItemSubtitle } from 'vuetify/components/VList';
import { VVirtualScroll } from 'vuetify/components/VVirtualScroll';
import { VIcon } from 'vuetify/components/VIcon';
import { VAvatar } from 'vuetify/components/VAvatar';
import { VImg } from 'vuetify/components/VImg';
import { VAlert } from 'vuetify/components/VAlert';
import { VChip } from 'vuetify/components/VChip';
import type { PluginCard } from '../features/plugin-forms';
import { pluginSearchMatches } from '../features/plugin-market';
import { safePluginIcon } from '../features/plugin-clone';
import PluginDialogCloseBtn from './PluginDialogCloseBtn.vue';
const props = defineProps<{ plugins: PluginCard[]; loading?: boolean; error?: string }>();
const emit = defineEmits<{ close: []; open: [plugin: PluginCard] }>();
const keyword = ref(''),
  display = useDisplay();
const results = computed(() =>
  props.plugins.filter((plugin) =>
    pluginSearchMatches(plugin, keyword.value),
  ),
);
</script>
<template>
  <VDialog
    :model-value="true"
    scrollable
    max-width="40rem"
    :fullscreen="!display.mdAndUp.value"
    :max-height="display.mdAndUp.value ? '85vh' : undefined"
    content-class="plugin-dialog-surface"
    @update:model-value="emit('close')"
  >
    <VCard>
      <VTextField
        v-model="keyword"
        label="搜索插件"
        placeholder="名称或说明"
        prepend-inner-icon="mdi-magnify"
        variant="outlined"
        class="ma-3 mr-12"
        hide-details
        clearable
        @update:model-value="keyword ||= ''"
      />
      <PluginDialogCloseBtn aria-label="关闭插件搜索" @click="emit('close')" />
      <VAlert v-if="error" type="error" variant="tonal">{{ error }}</VAlert>
      <p v-if="loading" class="pa-4" role="status">正在加载插件…</p>
      <VList lines="two"
        ><VVirtualScroll :items="results"
          ><template #default="{ item }">
            <VListItem :disabled="item.available === false" @click="emit('open', item)">
              <template #prepend
                ><VAvatar><VImg :src="safePluginIcon(item.icon || 'plugin.png')" /></VAvatar
              ></template>
              <VListItemTitle
                >{{ item.name }} <span class="text-caption">v{{ item.display_version || item.version }}</span
                ><VIcon
                  v-if="item.installed"
                  color="success"
                  icon="mdi-check-circle"
                  class="ms-2"
                  size="small"
              /></VListItemTitle>
              <VListItemSubtitle
                ><VChip
                  v-for="label in (item.plugin_label || '').split(',').filter(Boolean)"
                  :key="label"
                  size="small"
                  variant="tonal"
                  class="me-1"
                  >{{ label }}</VChip
                >{{ item.unavailable_reason || item.description }}</VListItemSubtitle
              >
            </VListItem>
          </template></VVirtualScroll
        ></VList
      >
      <p v-if="!loading && !results.length" class="pa-4">没有匹配的插件</p>
    </VCard>
  </VDialog>
</template>

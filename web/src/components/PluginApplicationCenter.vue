<script setup lang="ts">
import { computed } from 'vue';
import { VCard } from 'vuetify/components/VCard';
import { VList, VListSubheader, VListItem, VListItemTitle } from 'vuetify/components/VList';
import { VIcon } from 'vuetify/components/VIcon';
import { VAvatar } from 'vuetify/components/VAvatar';
import { VAlert } from 'vuetify/components/VAlert';
import { VBtn } from 'vuetify/components/VBtn';
import AppIcon from './AppIcon.vue';
import { pluginNavigationTarget, type PluginNavigationItem } from '../features/plugin-navigation';
const props = defineProps<{
  navigation: { label: string; section: string; items: string[][] }[];
  plugins: PluginNavigationItem[];
  error?: string;
  searchRoute: string;
}>();
const emit = defineEmits<{ retry: [] }>();
const groups = computed(() =>
  props.navigation
    .map((group) => ({
      title: group.label || '其他',
      items: [
        ...group.items
          .filter((item) => !['dashboard', 'discover', 'explore'].includes(item[0]!))
          .map((item) => ({
            title: item[2],
            icon: item[1],
            target: item[0] === 'search' ? props.searchRoute : item[0],
            plugin: false,
          })),
        ...props.plugins
          .filter((item) => item.section === group.section)
          .map((item) => ({
            title: item.title,
            icon: item.icon,
            target: pluginNavigationTarget(item),
            plugin: true,
          })),
      ],
    }))
    .filter((group) => group.items.length),
);
</script>
<template>
  <div class="plugin-application-center">
    <VAlert v-if="error" type="error" variant="tonal" class="mb-3"
      >{{ error }}<VBtn variant="text" @click="emit('retry')">重试</VBtn></VAlert
    >
    <section v-for="group in groups" :key="group.title" class="mb-3">
      <VListSubheader>{{ group.title }}</VListSubheader>
      <VCard variant="flat"
        ><VList class="pa-0"
          ><VListItem
            v-for="item in group.items"
            :key="item.target"
            :href="'#' + item.target"
            color="primary"
            class="application-item"
          >
            <template #prepend
              ><VAvatar size="42" color="primary" variant="text" class="me-3"
                ><VIcon v-if="item.plugin" :icon="item.icon" size="24" /><AppIcon
                  v-else
                  :name="item.icon!" /></VAvatar
            ></template>
            <VListItemTitle>{{ item.title }}</VListItemTitle
            ><template #append><VIcon icon="mdi-chevron-right" /></template> </VListItem></VList
      ></VCard>
    </section>
  </div>
</template>
<style scoped>
.plugin-application-center {
  max-width: 960px;
  margin: 0 auto;
}
.application-item {
  padding: 8px 12px;
}
.application-item:not(:last-child) {
  border-bottom: 1px solid rgba(var(--v-border-color), 0.12);
}
</style>

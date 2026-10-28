<script setup lang="ts">
import { computed } from 'vue';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
const props = defineProps<{ route: string }>();
const links = [
  { to: 'dashboard', title: '首页', icon: 'mdi-home-outline' },
  { to: 'discover', title: '推荐', icon: 'mdi-star-outline' },
  { to: 'explore', title: '探索', icon: 'mdi-apple-safari' },
  { to: 'apps', title: '更多', icon: 'mdi-dots-horizontal' },
];
const selected = computed(() =>
  props.route === 'dashboard'
    ? 'dashboard'
    : props.route === 'discover' || props.route.startsWith('recommend-')
      ? 'discover'
      : props.route.startsWith('explore')
        ? 'explore'
        : 'apps',
);
</script>
<template>
  <nav class="app-mode-footer" aria-label="应用导航">
    <div class="app-mode-footer-card">
      <VBtn
        v-for="link in links"
        :key="link.to"
        :href="'#' + link.to"
        :active="selected === link.to"
        :color="selected === link.to ? 'primary' : 'secondary'"
        variant="text"
        rounded="pill"
        class="app-mode-footer-button"
        ><span
          ><VIcon :icon="link.icon" size="24" /><span>{{ link.title }}</span></span
        ></VBtn
      >
    </div>
  </nav>
</template>
<style scoped>
.app-mode-footer {
  position: fixed;
  z-index: 1999;
  inset: auto 0 0;
  display: flex;
  justify-content: center;
  padding-bottom: calc(6px + env(safe-area-inset-bottom, 0px));
  pointer-events: none;
}
.app-mode-footer-card {
  display: flex;
  padding: 4px 6px;
  border-radius: 9999px;
  backdrop-filter: blur(24px);
  background: rgba(var(--v-theme-surface), 0.85);
  box-shadow: 0 3px 10px #0002;
  border: 1px solid rgba(var(--v-border-color), 0.12);
  pointer-events: auto;
}
.app-mode-footer-button {
  min-width: 68px;
  height: 48px;
}
.app-mode-footer-button :deep(.v-btn__content) > span {
  display: flex;
  flex-direction: column;
  align-items: center;
  font-size: 12px;
  gap: 2px;
}
</style>

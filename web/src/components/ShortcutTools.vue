<script setup lang="ts">
import { ref, defineAsyncComponent } from 'vue';
import { useDisplay } from 'vuetify';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { VMenu } from 'vuetify/components/VMenu';
import { VDialog } from 'vuetify/components/VDialog';
import { VCard, VCardItem, VCardTitle, VCardText } from 'vuetify/components/VCard';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VDivider } from 'vuetify/components/VDivider';
const PluginServices = defineAsyncComponent(() => import('./PluginServices.vue'));
const menu = ref(false),
  services = ref(false),
  display = useDisplay();
function openServices() {
  menu.value = false;
  services.value = true;
}
</script>
<template>
  <VThemeProvider theme="light" class="plugin-surface shortcut-tools">
    <VMenu
      v-model="menu"
      max-width="400"
      width="100%"
      max-height="560"
      location="top end"
      origin="top end"
      close-on-content-click
      close-on-back
      scrim
      content-class="plugin-dialog-surface shortcut-tools-menu"
    >
      <template #activator="{ props }"
        ><VBtn v-bind="props" variant="text" icon aria-label="捷径" title="捷径"
          ><VIcon icon="mdi-card-multiple-outline" /></VBtn
      ></template>
      <VCard class="shortcut-menu-card">
        <VCardItem class="shortcut-menu-heading"
          ><VCardTitle>捷径</VCardTitle
          ><template #append
            ><VBtn variant="text" icon size="small" aria-label="关闭捷径" @click="menu = false"
              ><VIcon icon="mdi-close" /></VBtn></template
        ></VCardItem>
        <VDivider />
        <div class="shortcut-grid">
          <VCard
            flat
            hover
            class="shortcut-tool"
            role="button"
            tabindex="0"
            @click="openServices"
            @keydown.enter.prevent="openServices"
            @keydown.space.prevent="openServices"
            ><VIcon color="primary" icon="mdi-list-box" size="24" />
            <div>
              <div class="shortcut-name">服务</div>
              <div class="shortcut-subtitle">定时服务</div>
            </div></VCard
          >
        </div>
      </VCard>
    </VMenu>
    <VDialog
      v-if="services"
      v-model="services"
      max-width="60rem"
      :fullscreen="!display.mdAndUp.value"
      scrollable
      content-class="plugin-dialog-surface plugin-service-dialog"
    >
      <VCard
        ><VCardItem class="shortcut-dialog-heading"
          ><VCardTitle><VIcon icon="mdi-list-box" class="shortcut-title-icon" />定时服务</VCardTitle
          ><template #append
            ><VBtn icon variant="text" aria-label="关闭定时服务" @click="services = false"
              ><VIcon icon="mdi-close" /></VBtn></template></VCardItem
        ><VDivider /><VCardText class="shortcut-service-body"><PluginServices /></VCardText
      ></VCard>
    </VDialog>
  </VThemeProvider>
</template>
<style scoped>
.shortcut-tools {
  display: contents;
}
.shortcut-menu-heading,
.shortcut-dialog-heading {
  padding: 12px 20px;
}
.shortcut-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  padding: 12px;
}
.shortcut-tool {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.12);
  cursor: pointer;
}
.shortcut-name {
  font-size: 16px;
  font-weight: 500;
}
.shortcut-subtitle {
  color: rgba(var(--v-theme-on-surface), 0.6);
  font-size: 12px;
}
.shortcut-service-body {
  padding: 0 !important;
}
.shortcut-title-icon {
  margin-right: 8px;
}
</style>

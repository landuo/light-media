<script setup lang="ts">
import { ref } from 'vue';
import { VMenu } from 'vuetify/components/VMenu';
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';
import { VList, VListItem, VListItemTitle, VListItemSubtitle } from 'vuetify/components/VList';
import { interfaceMode, setInterfaceMode, type InterfaceMode } from '../features/interface-mode';
defineProps<{ labeled?: boolean }>();
const menu = ref(false),
  layoutMenu = ref(false);
const modes: { value: InterfaceMode; title: string; icon: string }[] = [
  { value: 'auto', title: '自动', icon: 'mdi-devices' },
  { value: 'desktop', title: '桌面', icon: 'mdi-monitor' },
  { value: 'app', title: '移动端', icon: 'mdi-cellphone' },
];
function choose(value: InterfaceMode) {
  setInterfaceMode(value);
  layoutMenu.value = false;
}
</script>
<template>
  <VMenu
    v-model="menu"
    location="bottom end"
    :close-on-content-click="false"
    content-class="plugin-dialog-surface"
  >
    <template #activator="{ props }"
      ><VBtn
        v-bind="props"
        :icon="labeled ? undefined : 'mdi-account-circle-outline'"
        :prepend-icon="labeled ? 'mdi-devices' : undefined"
        :text="labeled ? '界面布局' : undefined"
        variant="text"
        aria-label="用户界面设置"
    /></template>
    <VList min-width="220">
      <VMenu
        v-model="layoutMenu"
        location="end"
        :close-on-content-click="true"
        content-class="plugin-dialog-surface"
      >
        <template #activator="{ props }"
          ><VListItem v-bind="props" prepend-icon="mdi-devices" append-icon="mdi-chevron-right"
            ><VListItemTitle>界面布局</VListItemTitle
            ><VListItemSubtitle>{{
              modes.find((mode) => mode.value === interfaceMode)?.title
            }}</VListItemSubtitle></VListItem
          ></template
        >
        <VList min-width="200"
          ><VListItem
            v-for="mode in modes"
            :key="mode.value"
            :active="interfaceMode === mode.value"
            :prepend-icon="mode.icon"
            @click="choose(mode.value)"
            ><VListItemTitle>{{ mode.title }}</VListItemTitle
            ><template #append
              ><VIcon
                v-if="interfaceMode === mode.value"
                icon="mdi-check"
                color="primary"
                size="small" /></template></VListItem
        ></VList>
      </VMenu>
    </VList>
  </VMenu>
</template>

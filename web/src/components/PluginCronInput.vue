<script setup lang="ts">
import {
  defineAsyncComponent,
  getCurrentInstance,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from 'vue';
import { VMenu } from 'vuetify/components/VMenu';
import { VList, VListItem } from 'vuetify/components/VList';
import { VRow, VCol } from 'vuetify/components/VGrid';
import { VChip } from 'vuetify/components/VChip';
import { VIcon } from 'vuetify/components/VIcon';
import '@vue-js-cron/vuetify/dist/vuetify.css';

const CronVuetify = defineAsyncComponent(async () => {
  return (await import('@vue-js-cron/vuetify')).CronVuetify;
});
const props = withDefaults(defineProps<{ modelValue?: string | null }>(), {
  modelValue: '* * * * *',
});
const emit = defineEmits<{ 'update:modelValue': [value: string | null] }>();
const instance = getCurrentInstance();
// The original cron library resolves these by name in its nested components.
// Register only its dependencies, when this asynchronous field is loaded.
for (const [name, component] of Object.entries({
  VRow,
  VCol,
  VList,
  VListItem,
  VMenu,
  VChip,
  VIcon,
})) {
  if (instance && !instance.appContext.app.component(name))
    instance.appContext.app.component(name, component);
}
const menu = ref(false);
const currentCron = ref(props.modelValue);
const menuRoot = ref<HTMLElement>();
const menuContentClass = `cron-input-menu-${instance?.uid ?? 'default'}`;

function isCronMenuTarget(target: EventTarget | null) {
  if (!(target instanceof Element)) return false;
  if (menuRoot.value?.contains(target)) return true;
  const content = document.querySelector(`.${menuContentClass}`);
  if (content?.contains(target)) return true;
  const overlayId = target.closest('.v-overlay')?.getAttribute('id');
  if (!overlayId || !content) return false;
  return Array.from(content.querySelectorAll('[aria-owns]')).some(
    (activator) => activator.getAttribute('aria-owns') === overlayId,
  );
}
function closeOnOutsidePointerDown(event: PointerEvent) {
  if (menu.value && !isCronMenuTarget(event.target)) menu.value = false;
}
onMounted(() => document.addEventListener('pointerdown', closeOnOutsidePointerDown, true));
onBeforeUnmount(() => document.removeEventListener('pointerdown', closeOnOutsidePointerDown, true));
watch(currentCron, (value) => emit('update:modelValue', value));
watch(
  () => props.modelValue,
  (value) => {
    currentCron.value = value;
  },
);
</script>

<template>
  <div ref="menuRoot">
    <VMenu
      v-model="menu"
      :close-on-content-click="false"
      :content-class="['cursor-default', menuContentClass]"
      persistent
    >
      <template #activator="{ props: menuProps }">
        <slot name="activator" :menuprops="menuProps" />
      </template>
      <VList>
        <VListItem>
          <CronVuetify
            :model-value="currentCron ?? undefined"
            locale="zh-CN"
            :chip-props="{ color: 'success' }"
            class="mt-1"
            @update:model-value="currentCron = $event"
          />
        </VListItem>
      </VList>
    </VMenu>
  </div>
</template>

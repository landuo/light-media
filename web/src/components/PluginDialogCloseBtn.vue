<script setup lang="ts">
import { VBtn } from 'vuetify/components/VBtn';
import { VIcon } from 'vuetify/components/VIcon';

defineProps<{ modelValue?: boolean; innerClass?: string }>();
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; click: [] }>();
function close() {
  emit('update:modelValue', false);
  emit('click');
}
</script>
<template>
  <VBtn
    :class="innerClass || 'plugin-nested-dialog-close'"
    variant="text"
    color="default"
    icon
    aria-label="关闭"
    @click.stop="close"
    ><VIcon icon="mdi-close" size="24"
  /></VBtn>
</template>
<style scoped>
.plugin-nested-dialog-close {
  position: absolute;
  inset-block-start: 0.75rem;
  inset-inline-end: 0.75rem;
  z-index: 10;
}
</style>

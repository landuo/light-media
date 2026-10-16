<script setup lang="ts">
import { VAutocomplete } from 'vuetify/components/VAutocomplete';
import { h } from 'vue';
import AppIcon from './AppIcon.vue';
import {
  qualityOptions,
  resolutionOptions,
  effectOptions,
} from '../features/subscription-filter-options';
import { subscriptionFilterValue, setSubscriptionFilter } from '../features/subscription-filters';
defineProps<{ modelValue: Record<string, any> }>();
const emit = defineEmits<{ 'update:modelValue': [value: Record<string, any>] }>();
const fields = [
  { key: 'quality', label: '质量', hint: '订阅资源质量', icon: 'quality', options: qualityOptions },
  {
    key: 'resolution',
    label: '分辨率',
    hint: '订阅资源分辨率',
    icon: 'tv',
    options: resolutionOptions,
  },
  { key: 'effect', label: '特效', hint: '订阅资源特效', icon: 'sparkles', options: effectOptions },
] as const;
const icons = {
  quality: () => h(AppIcon, { name: 'quality' }),
  resolution: () => h(AppIcon, { name: 'tv' }),
  effect: () => h(AppIcon, { name: 'sparkles' }),
};
function items(field: (typeof fields)[number], policy: Record<string, any>) {
  const value = subscriptionFilterValue(policy, field.key);
  return value && !field.options.some((item) => item.value === value)
    ? [...field.options, { title: value, value }]
    : field.options;
}
</script>
<template>
  <div class="subscription-original-filters">
    <VAutocomplete
      v-for="field in fields"
      :key="field.key"
      :model-value="subscriptionFilterValue(modelValue, field.key)"
      :label="field.label"
      :items="items(field, modelValue)"
      :hint="field.hint"
      :prepend-inner-icon="icons[field.key]"
      persistent-hint
      variant="outlined"
      @update:model-value="
        emit('update:modelValue', setSubscriptionFilter(modelValue, field.key, $event))
      "
    />
  </div>
</template>
<style scoped>
.subscription-original-filters {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  margin-top: 24px;
}
.subscription-original-filters :deep(.v-label) {
  flex-direction: row;
  gap: 0;
}
.subscription-original-filters :deep(.v-field input) {
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
  color: inherit;
  font: inherit;
  line-height: inherit;
  min-width: 0;
}
.subscription-original-filters :deep(.v-field__input > input) {
  padding: 0;
}
.subscription-original-filters :deep(.v-field__prepend-inner) {
  padding-right: 8px;
  color: rgba(var(--v-theme-on-surface), 0.54);
}
@media (max-width: 959px) {
  .subscription-original-filters {
    grid-template-columns: 1fr;
  }
}
</style>

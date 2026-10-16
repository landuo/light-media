<script setup lang="ts">
import { computed, ref, useAttrs, watch } from 'vue';
import { VTextField } from 'vuetify/components/VTextField';
import PluginCronInput from './PluginCronInput.vue';

defineOptions({ inheritAttrs: false });
const attrs = useAttrs();
const props = withDefaults(defineProps<{ modelValue?: string | null }>(), {
  modelValue: '* * * * *',
});
const emit = defineEmits<{ 'update:modelValue': [value: string | null] }>();
const innerValue = ref(props.modelValue);
watch(
  () => props.modelValue,
  (value) => {
    innerValue.value = value;
  },
);
const fieldProps = computed(() => ({ ...attrs }));
function updateModelValue(value: string | null) {
  innerValue.value = value;
  emit('update:modelValue', value);
}
</script>

<template>
  <PluginCronInput v-model="innerValue" @update:model-value="updateModelValue">
    <template #activator="{ menuprops }">
      <VTextField
        :model-value="innerValue"
        v-bind="{ ...menuprops, ...fieldProps }"
        clearable
        @update:model-value="updateModelValue"
      />
    </template>
  </PluginCronInput>
</template>

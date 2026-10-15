<script setup lang="ts">
import AppIcon from './AppIcon.vue';
defineProps<{ label: string; icon: string; hint?: string }>();
</script>

<template>
  <label class="subscription-field">
    <span class="subscription-field-label">{{ label }}</span>
    <div class="subscription-field-control">
      <AppIcon :name="icon" />
      <slot />
    </div>
    <small v-if="hint">{{ hint }}</small>
  </label>
</template>

<style scoped>
.subscription-field {
  position: relative;
  display: flex;
  min-width: 0;
  gap: 4px;
  color: var(--text);
}
.subscription-field-label {
  position: absolute;
  z-index: 1;
  top: -8px;
  left: 12px;
  max-width: calc(100% - 24px);
  padding: 0 4px;
  background: var(--surface);
  color: var(--muted);
  font-size: 12px;
  line-height: 16px;
}
.subscription-field-control {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 56px;
  border: 1px solid rgba(58, 53, 65, 0.22);
  border-radius: 6px;
  padding: 0 12px;
}
.subscription-field-control:hover {
  border-color: var(--muted);
}
.subscription-field-control:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}
.subscription-field:focus-within .subscription-field-label {
  color: var(--accent);
}
.subscription-field-control > :deep(.app-icon) {
  width: 24px;
  height: 24px;
  color: var(--muted);
}
.subscription-field-control :deep(input),
.subscription-field-control :deep(select),
.subscription-field-control :deep(textarea) {
  flex: 1;
  width: 100%;
  min-width: 0;
  min-height: 54px;
  padding: 10px 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
  color: var(--text);
  font-size: 16px;
}
.subscription-field small {
  margin: 0 12px;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
}
</style>

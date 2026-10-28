<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { defaultIcon, siteIconURL } from '../features/site-icons';

const props = defineProps<{ url: string; name: string; cached?: string }>();
const failed = ref(false);
const cachedFailed = ref(false);
const source = computed(() =>
  props.cached && !cachedFailed.value
    ? props.cached
    : failed.value
      ? defaultIcon
      : siteIconURL(props.url),
);
function imageFailed() {
  if (props.cached && !cachedFailed.value) cachedFailed.value = true;
  else failed.value = true;
}
watch(
  () => [props.url, props.cached],
  () => {
    failed.value = false;
    cachedFailed.value = false;
  },
);
</script>
<template>
  <img
    class="site-icon"
    :src="source"
    :alt="name"
    width="32"
    height="32"
    loading="lazy"
    decoding="async"
    referrerpolicy="no-referrer"
    @error="imageFailed"
  />
</template>
<style scoped>
.site-icon {
  display: block;
  flex: none;
  width: 32px;
  height: 32px;
  object-fit: contain;
  border-radius: 6px;
}
</style>

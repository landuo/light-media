<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { defaultIcon, siteIconURL } from '../features/site-icons';

const props = defineProps<{ url: string; name: string }>();
const failed = ref(false);
const source = computed(() => (failed.value ? defaultIcon : siteIconURL(props.url)));
watch(
  () => props.url,
  () => {
    failed.value = false;
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
    @error="failed = true"
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

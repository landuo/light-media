<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue';
import { VAlert } from 'vuetify/components/VAlert';
import { VBtn } from 'vuetify/components/VBtn';
import { VCard, VCardText, VCardTitle } from 'vuetify/components/VCard';
import { VDialog } from 'vuetify/components/VDialog';
import { VSkeletonLoader } from 'vuetify/components/VSkeletonLoader';
import { VThemeProvider } from 'vuetify/components/VThemeProvider';
import { apiURL } from '../base-path';
import { createPluginAuth } from '../features/plugin-auth';
import { loadPluginRemote } from '../features/plugin-federation';
import { createPluginRemoteAPI } from '../features/plugin-remote-api';

const emit = defineEmits<{ authenticated: [] }>();
async function request(path: string, signal: AbortSignal, body?: unknown) {
  const response = await fetch(apiURL(path), {
    method: body === undefined ? 'GET' : 'POST', credentials: 'same-origin', redirect: 'error', signal,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const value = await response.json();
  if (!response.ok || value?.error || value?.success === false)
    throw new Error(value?.error?.message || value?.message || '插件认证失败');
  return value?.data;
}
const auth = createPluginAuth({
  providers: signal => request('/auth/providers', signal),
  load: loadPluginRemote,
  exchange: (ticket, signal) => request('/auth/exchange', signal, { ticket }),
  authenticated: () => emit('authenticated'),
});
const state = auth.state;
const remoteAPI = computed(() => state.selected && state.signal && state.sourcePluginID
  ? createPluginRemoteAPI(state.sourcePluginID, state.selected.plugin_id, state.signal)
  : undefined);
onMounted(auth.refresh);
onUnmounted(auth.destroy);
</script>
<template>
  <div v-if="state.providers.length || state.providersError" class="plugin-auth-entries">
    <VBtn v-for="provider in state.providers" :key="provider.id" type="button" block variant="outlined"
      color="primary" class="mt-3" :prepend-icon="provider.icon || 'mdi-login-variant'"
      :loading="state.loading && state.selected?.id === provider.id" @click="auth.open(provider)">
      {{ provider.name }}
    </VBtn>
    <VAlert v-if="state.providersError" type="error" variant="tonal" class="mt-3">
      {{ state.providersError }}
      <VBtn type="button" variant="text" @click="auth.refresh">重试</VBtn>
    </VAlert>
  </div>
  <VDialog :model-value="!!state.selected" max-width="520" persistent @update:model-value="!$event && auth.close()">
    <VThemeProvider theme="light" with-background>
      <VCard class="plugin-surface plugin-dialog-surface">
        <VCardTitle class="d-flex align-center justify-space-between">
          {{ state.selected?.name }}
          <VBtn type="button" icon="mdi-close" variant="text" aria-label="关闭认证" @click="auth.close" />
        </VCardTitle>
        <VCardText>
          <VSkeletonLoader v-if="state.loading && !state.component" type="article" />
          <VAlert v-if="state.error" type="error" variant="tonal" class="mb-3">
            {{ state.error }}
            <VBtn type="button" variant="text" :disabled="state.loading || state.exchanging" @click="auth.retry">刷新并重试</VBtn>
          </VAlert>
          <component v-if="state.component && state.selected" :is="state.component" :api="remoteAPI"
            :provider="state.selected" :plugin-id="state.selected.plugin_id"
            @authenticated="auth.authenticate" @error="auth.fail" @close="auth.close" />
        </VCardText>
      </VCard>
    </VThemeProvider>
  </VDialog>
</template>

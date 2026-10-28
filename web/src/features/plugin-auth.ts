import { shallowReactive, type Component } from 'vue';
import { validPluginComponent, validPluginInstance, type PluginFrontendDescriptor } from './plugin-remote-contract.ts';

export type PluginAuthProvider = {
  id: string;
  type: 'plugin';
  plugin_id: string;
  name: string;
  icon?: string;
  enabled?: unknown;
  component?: string;
  remote: { id: string; url: string };
  [key: string]: unknown;
};
export function pluginAuthProviders(value: unknown): PluginAuthProvider[] {
  if (!Array.isArray(value)) throw new Error('认证入口响应无效');
  return value.filter((item): item is PluginAuthProvider => {
    if (!item || typeof item !== 'object' || item.type !== 'plugin' || item.enabled === false ||
        typeof item.id !== 'string' || typeof item.name !== 'string' ||
        !validPluginInstance(item.plugin_id) || item.remote?.id !== item.plugin_id ||
        typeof item.remote.url !== 'string') return false;
    return validPluginComponent(item.component || 'AuthPage');
  });
}

type Dependencies = {
  providers: (signal: AbortSignal) => Promise<unknown>;
  load: (instance: string, component: string, options: { signal: AbortSignal }) => Promise<{ component: Component; descriptor: PluginFrontendDescriptor }>;
  exchange: (ticket: string, signal: AbortSignal) => Promise<unknown>;
  authenticated: () => void | Promise<void>;
};
export function createPluginAuth(deps: Dependencies) {
  const state = shallowReactive({
    providers: [] as PluginAuthProvider[], selected: null as PluginAuthProvider | null,
    sourcePluginID: '',
    component: null as Component | null, loading: false, exchanging: false,
    error: '', providersError: '', signal: undefined as AbortSignal | undefined,
  });
  let generation = 0;
  let listGeneration = 0;
  let active = true;
  let controller: AbortController | undefined;
  let listController: AbortController | undefined;
  const message = (error: unknown) => error instanceof Error ? error.message : String(error || '认证失败');
  function close() {
    generation++;
    controller?.abort();
    controller = undefined;
    state.selected = null;
    state.component = null;
    state.sourcePluginID = '';
    state.signal = undefined;
    state.loading = false;
    state.exchanging = false;
    state.error = '';
  }
  async function refresh() {
    const current = ++listGeneration;
    listController?.abort();
    listController = new AbortController();
    state.providersError = '';
    try {
      const providers = pluginAuthProviders(await deps.providers(listController.signal));
      if (active && current === listGeneration) state.providers = providers;
    } catch (error) {
      if (active && current === listGeneration) {
        state.providers = [];
        state.providersError = message(error);
      }
    }
  }
  async function open(provider: PluginAuthProvider) {
    close();
    if (!active || !state.providers.includes(provider)) return;
    const current = generation;
    controller = new AbortController();
    state.signal = controller.signal;
    state.selected = provider;
    state.loading = true;
    try {
      const result = await deps.load(provider.plugin_id, provider.component || 'AuthPage', { signal: controller.signal });
      if (!active || current !== generation) return;
      if (result.descriptor.instance_id !== provider.plugin_id || result.descriptor.entry_path !== provider.remote.url)
        throw new Error('认证插件版本已变化，请关闭后刷新入口');
      state.sourcePluginID = result.descriptor.plugin_id;
      state.component = result.component;
    } catch (error) {
      if (active && current === generation) state.error = message(error);
    } finally {
      if (active && current === generation) state.loading = false;
    }
  }
  async function authenticate(payload: unknown) {
    if (!active || !state.selected || !state.component || !controller || state.exchanging) return;
    const ticket = payload && typeof payload === 'object' ? (payload as { ticket?: unknown }).ticket : undefined;
    if (typeof ticket !== 'string' || !ticket) {
      state.error = '认证插件没有返回有效票据';
      return;
    }
    const current = generation;
    state.exchanging = true;
    state.error = '';
    try {
      const response = await deps.exchange(ticket, controller.signal) as { authenticated?: unknown } | null;
      if (!active || current !== generation) return;
      if (response?.authenticated !== true) throw new Error('认证未完成，请重试');
      close();
      await deps.authenticated();
    } catch (error) {
      if (active && current === generation) state.error = message(error);
    } finally {
      if (active && current === generation) state.exchanging = false;
    }
  }
  return {
    state, refresh, open, close, authenticate,
    retry: async () => {
      const selected = state.selected;
      if (!selected || state.loading || state.exchanging) return;
      close();
      const current = generation;
      await refresh();
      if (!active || generation !== current) return;
      const updated = state.providers.find(item => item.id === selected.id && item.plugin_id === selected.plugin_id);
      if (updated) await open(updated);
    },
    fail: (error: unknown) => { if (state.selected) state.error = message(error); },
    destroy: () => { active = false; listGeneration++; listController?.abort(); close(); },
  };
}

import { reactive } from 'vue';
import { validPluginInstance } from './plugin-remote-contract.ts';

export type PluginNavigationItem = {
  plugin_id: string;
  instance_id: string;
  nav_key: string;
  title: string;
  icon: string;
  section: string;
  permission: string | null;
  order: number;
};

// Same cache/retry behavior as the original pluginSidebarNav store. An epoch
// also prevents a request started before logout from populating a new session.
export function createPluginNavigation(fetchItems: () => Promise<PluginNavigationItem[]>) {
  const state = reactive({ items: [] as PluginNavigationItem[], loaded: false });
  let inflight: Promise<void> | undefined;
  let epoch = 0;
  function reset() {
    epoch++;
    state.items = [];
    state.loaded = false;
    inflight = undefined;
  }
  function ensure(force = false): Promise<void> {
    if (!force && state.loaded) return Promise.resolve();
    if (inflight) return inflight;
    const current = epoch;
    const request = (async () => {
      for (let attempt = 0; attempt < 2; attempt++) {
        try {
          const items = await fetchItems();
          if (current !== epoch) return;
          state.items = Array.isArray(items) ? items.filter((item) => validPluginInstance(item?.instance_id) && validPluginNavKey(item?.nav_key)) : [];
          state.loaded = true;
          return;
        } catch {
          if (current !== epoch) return;
          if (attempt === 0) await new Promise((resolve) => setTimeout(resolve, 500));
        }
      }
      if (current === epoch) state.items = [];
    })();
    inflight = request;
    request.finally(() => {
      if (inflight === request) inflight = undefined;
    });
    return request;
  }
  return { state, ensure, reset };
}

export function validPluginNavKey(value: unknown): value is string {
  return typeof value === 'string' && !!value && value.length <= 256 &&
    value !== '.' && value !== '..' && !/[\\/?#\s\u0000-\u001f\u007f]/.test(value);
}
export function pluginNavigationTarget(item: PluginNavigationItem) {
  if (!validPluginInstance(item.instance_id) || !validPluginNavKey(item.nav_key))
    throw new Error('插件全页入口无效');
  return 'plugin-app/' + encodeURIComponent(item.instance_id) + '/' + encodeURIComponent(item.nav_key);
}
export function parsePluginNavigationRoute(route: string) {
  const parts = route.split('/');
  if (parts.length !== 3 || parts[0] !== 'plugin-app') throw new Error('插件全页入口无效');
  let instance: string, navKey: string;
  try { instance = decodeURIComponent(parts[1]!); navKey = decodeURIComponent(parts[2]!); }
  catch { throw new Error('插件全页入口无效'); }
  if (!validPluginInstance(instance) || !validPluginNavKey(navKey)) throw new Error('插件全页入口无效');
  return { instance, navKey };
}

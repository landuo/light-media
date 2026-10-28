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
  const state = reactive({ items: [] as PluginNavigationItem[], loaded: false, error: '' });
  let inflight: Promise<void> | undefined;
  let epoch = 0;
  function reset() {
    epoch++;
    state.items = [];
    state.loaded = false;
    state.error = '';
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
          if (!Array.isArray(items)) throw new Error('插件导航返回格式无效');
          state.items = Array.isArray(items)
            ? items.filter(
                (item) =>
                  validPluginInstance(item?.instance_id) && validPluginNavKey(item?.nav_key),
              )
            : [];
          state.loaded = true;
          state.error = '';
          return;
        } catch (error) {
          if (current !== epoch) return;
          state.error = error instanceof Error ? error.message : '插件导航读取失败';
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
  // This is a hash-route value, not an asset or filesystem path. The fixed
  // backend rejects exactly slash/query/fragment/ASCII space; dots and other
  // whitespace may still select the AppPage/Page fallback.
  return typeof value === 'string' && !!value && !/[/?# ]/.test(value);
}
export function pluginNavigationTarget(item: PluginNavigationItem) {
  if (!validPluginInstance(item.instance_id) || !validPluginNavKey(item.nav_key))
    throw new Error('插件全页入口无效');
  return (
    'plugin-app/' + encodeURIComponent(item.instance_id) + '/' + encodeURIComponent(item.nav_key)
  );
}
export function parsePluginNavigationRoute(route: string) {
  const parts = route.split('/');
  if (![2, 3].includes(parts.length) || parts[0] !== 'plugin-app')
    throw new Error('插件全页入口无效');
  let instance: string, navKey: string;
  try {
    instance = decodeURIComponent(parts[1]!);
    navKey = parts[2] ? decodeURIComponent(parts[2]) : 'main';
  } catch {
    throw new Error('插件全页入口无效');
  }
  if (!validPluginInstance(instance) || !validPluginNavKey(navKey))
    throw new Error('插件全页入口无效');
  return { instance, navKey };
}

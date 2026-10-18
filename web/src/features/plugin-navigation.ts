import { reactive } from 'vue';

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
          state.items = Array.isArray(items) ? items : [];
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

export function pluginNavigationTarget(item: PluginNavigationItem) {
  if (
    item.plugin_id !== 'SeedRescue' ||
    item.nav_key !== 'main' ||
    !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$/.test(item.instance_id)
  )
    throw new Error('插件全页入口无效');
  return 'plugin-app/' + encodeURIComponent(item.instance_id) + '/main';
}

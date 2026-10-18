import * as Vue from 'vue';
import { apiURL } from '../base-path.ts';

type ComponentName = 'Config' | 'Page' | 'Dashboard' | 'AppPage';
type FederationEntry = {
  init: (scope: Record<string, unknown>) => void;
  get: (name: string) => Promise<() => Vue.Component>;
};
const components = new Set(['Config', 'Page', 'Dashboard', 'AppPage']);
const entries = new Map<string, Promise<FederationEntry>>();

export function pluginFederationEntry(pluginID: string, instance: string, name: string) {
  if (
    pluginID !== 'SeedRescue' ||
    !components.has(name) ||
    !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$/.test(instance)
  )
    throw new Error('此插件尚未提供原版 Vue 界面');
  return apiURL('/plugins/' + encodeURIComponent(instance) + '/frontend/assets/remoteEntry.js');
}

export async function loadPluginRemote(pluginID: string, instance: string, name: ComponentName) {
  const entryURL = pluginFederationEntry(pluginID, instance, name);
  let pending = entries.get(entryURL);
  if (!pending) {
    pending = import(/* @vite-ignore */ entryURL).then((entry: FederationEntry) => {
      if (typeof entry.init !== 'function' || typeof entry.get !== 'function')
        throw new Error('插件界面入口无效');
      // The unchanged original assets importShared('vue'). Supply exactly the
      // application's Vue module so reactivity and component context are shared.
      entry.init({
        vue: { [Vue.version]: { get: async () => () => Vue, loaded: true, scope: 'default' } },
      });
      return entry;
    });
    entries.set(entryURL, pending);
    pending.catch(() => entries.delete(entryURL));
  }
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      pending.then(async (entry) => (await entry.get('./' + name))()),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error('插件界面加载超时，请重试')), 20000);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

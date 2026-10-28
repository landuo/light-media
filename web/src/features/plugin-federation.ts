import * as Vue from 'vue';
import * as Vuetify from 'vuetify';
import { apiURL } from '../base-path.ts';
import {
  parsePluginFrontendDescriptor,
  validPluginInstance,
  type PluginComponentName,
  type PluginFrontendDescriptor,
} from './plugin-remote-contract.ts';

type FederationEntry = {
  init: (scope: Record<string, unknown>) => void | Promise<void>;
  get: (name: string) => Promise<() => unknown>;
};
type LoadOptions = { signal?: AbortSignal; version?: string };
type LoaderDependencies = {
  fetcher?: typeof fetch;
  importEntry?: (url: string) => Promise<unknown>;
  timeoutMs?: number;
};
export function pluginFederationEntry(
  descriptor: PluginFrontendDescriptor,
  name: PluginComponentName,
) {
  const checked = parsePluginFrontendDescriptor(descriptor, descriptor.instance_id, name);
  return apiURL(checked.entry_path);
}
export function pluginRemoteSharedScope(shared: PluginFrontendDescriptor['shared']) {
  if (
    !Array.isArray(shared) ||
    !shared.includes('vue') ||
    new Set(shared).size !== shared.length ||
    shared.some((name) => name !== 'vue' && name !== 'vuetify')
  )
    throw new Error('插件共享依赖不受支持');
  const scope: Record<string, unknown> = {
    vue: { [Vue.version]: { get: async () => () => Vue, loaded: true, scope: 'default' } },
  };
  if (shared.includes('vuetify'))
    scope.vuetify = {
      [Vuetify.version]: { get: async () => () => Vuetify, loaded: true, scope: 'default' },
    };
  return scope;
}
function abortError() {
  return new DOMException('插件界面加载已取消', 'AbortError');
}

// Dependency injection exercises the real loader in tests, without replacing
// its contract validation, identity checks, cancellation or immutable cache.
export function createPluginRemoteLoader(dependencies: LoaderDependencies = {}) {
  const entries = new Map<string, Promise<FederationEntry>>();
  const importEntry = dependencies.importEntry ?? ((url: string) => import(/* @vite-ignore */ url));
  return async function load(
    instance: string,
    name: PluginComponentName,
    options: LoadOptions = {},
  ) {
    if (!validPluginInstance(instance)) throw new Error('插件实例无效');
    if (options.signal?.aborted) throw abortError();
    const controller = new AbortController();
    let rejectStopped!: (reason: Error) => void;
    const stopped = new Promise<never>((_, reject) => {
      rejectStopped = reject;
    });
    const cancel = () => {
      controller.abort();
      rejectStopped(abortError());
    };
    options.signal?.addEventListener('abort', cancel, { once: true });
    const timer = setTimeout(() => {
      rejectStopped(new Error('插件界面加载超时，请重试'));
      controller.abort();
    }, dependencies.timeoutMs ?? 20000);
    try {
      return await Promise.race([
        stopped,
        (async () => {
          // Original federation retries by re-discovering and registering once.
          // Both attempts share the caller's timeout and cancellation boundary.
          for (let attempt = 0; ; attempt++) {
            let entryKey: string | undefined;
            try {
              const response = await (dependencies.fetcher ?? fetch)(
                apiURL('/plugins/' + encodeURIComponent(instance) + '/frontend'),
                {
                  signal: controller.signal,
                  credentials: 'same-origin',
                  cache: 'no-store',
                  redirect: 'error',
                },
              );
              if (response.status === 401) globalThis.dispatchEvent?.(new Event('session-expired'));
              let result: any;
              try {
                result = await response.json();
              } catch {
                throw new Error('插件界面元数据响应无法读取');
              }
              if (!response.ok || result?.error || result?.success === false)
                throw new Error(
                  result?.error?.message || result?.message || '无法读取插件界面元数据',
                );
              if (controller.signal.aborted) throw abortError();
              const descriptor = parsePluginFrontendDescriptor(
                result?.data,
                instance,
                name,
                options.version,
              );
              const entryURL = pluginFederationEntry(descriptor, name);
              // The digest is part of the directory, not a query suffix: all relative
              // ESM and CSS imports retain the installed instance/package identity.
              const key = JSON.stringify([
                entryURL,
                descriptor.plugin_id,
                descriptor.version,
                descriptor.shared,
                descriptor.exposes,
              ]);
              entryKey = key;
              let pending = entries.get(key);
              if (!pending) {
                pending = (async () => {
                  const entry = (await importEntry(entryURL)) as FederationEntry;
                  if (!entry || typeof entry.init !== 'function' || typeof entry.get !== 'function')
                    throw new Error('插件界面入口无效');
                  await entry.init(pluginRemoteSharedScope(descriptor.shared));
                  return entry;
                })();
                entries.set(key, pending);
                const created = pending;
                pending.catch(() => {
                  if (entries.get(key) === created) entries.delete(key);
                });
              }
              const entry = await pending;
              if (controller.signal.aborted) throw abortError();
              const factory = await entry.get('./' + name);
              if (controller.signal.aborted) throw abortError();
              if (typeof factory !== 'function') throw new Error('插件界面组件工厂无效');
              const loaded = factory() as {
                __esModule?: unknown;
                [Symbol.toStringTag]?: unknown;
                default?: unknown;
              } | null;
              // Same unwrap rule as Vite federation's unwrapDefault helper.
              const component =
                loaded?.__esModule || loaded?.[Symbol.toStringTag] === 'Module'
                  ? loaded.default
                  : loaded;
              if (
                !component ||
                (typeof component !== 'function' &&
                  (typeof component !== 'object' || Array.isArray(component)))
              )
                throw new Error('插件界面组件无效');
              const definition = component as {
                setup?: unknown;
                render?: unknown;
                template?: unknown;
              };
              if (
                typeof component !== 'function' &&
                typeof definition.setup !== 'function' &&
                typeof definition.render !== 'function' &&
                typeof definition.template !== 'string'
              )
                throw new Error('插件界面组件无效');
              return { component: component as Vue.Component, descriptor };
            } catch (error) {
              if (controller.signal.aborted || attempt >= 1) throw error;
              if (entryKey) entries.delete(entryKey);
            }
          }
        })(),
      ]);
    } finally {
      clearTimeout(timer);
      options.signal?.removeEventListener('abort', cancel);
    }
  };
}
export const loadPluginRemote = createPluginRemoteLoader();

export function pluginAppComponentCandidates(navKey = 'main'): string[] {
  const raw = (navKey || 'main').trim();
  if (!raw || raw.toLowerCase() === 'main') return ['AppPage', 'Page'];
  const pascal = raw
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1).toLowerCase())
    .join('');
  return [...(pascal ? ['AppPage' + pascal] : []), 'AppPage', 'Page'];
}

export async function loadPluginAppRemote(
  instance: string,
  navKey: string,
  options: LoadOptions = {},
  load = loadPluginRemote,
) {
  let lastError: unknown;
  for (const name of pluginAppComponentCandidates(navKey)) {
    if (options.signal?.aborted) throw abortError();
    try {
      return await load(instance, name, options);
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError ?? new Error('插件全页组件加载失败');
}

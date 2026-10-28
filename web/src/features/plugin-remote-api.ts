import axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from 'axios';
import { apiURL } from '../base-path.ts';
import { validPluginInstance } from './plugin-remote-contract.ts';

export class PluginRemoteError extends Error {
  constructor(
    message: string,
    public details?: unknown,
  ) {
    super(message);
  }
}
export type PluginRequestOptions = AxiosRequestConfig;
type DataMethods = {
  (config: AxiosRequestConfig): Promise<any>;
  (url: string, config?: AxiosRequestConfig): Promise<any>;
  request<T = any>(config: AxiosRequestConfig): Promise<T>;
  get<T = any>(path: string, options?: AxiosRequestConfig): Promise<T>;
  delete<T = any>(path: string, options?: AxiosRequestConfig): Promise<T>;
  head<T = any>(path: string, options?: AxiosRequestConfig): Promise<T>;
  options<T = any>(path: string, options?: AxiosRequestConfig): Promise<T>;
  post<T = any>(path: string, body?: unknown, options?: AxiosRequestConfig): Promise<T>;
  put<T = any>(path: string, body?: unknown, options?: AxiosRequestConfig): Promise<T>;
  patch<T = any>(path: string, body?: unknown, options?: AxiosRequestConfig): Promise<T>;
};
export type PluginRemoteAPI = Omit<AxiosInstance, keyof DataMethods> & DataMethods;
type RoutedRequest = InternalAxiosRequestConfig & { _pluginHostEnvelope?: boolean };
const activeRequests = new Set<AbortController>();
export function abortPluginAPIRequests() {
  for (const controller of activeRequests) if (!controller.signal.aborted) controller.abort();
  activeRequests.clear();
}
function cleanupRequest(config?: AxiosRequestConfig) {
  const controller = [...activeRequests].find((item) => item.signal === config?.signal);
  if (controller) activeRequests.delete(controller);
}

function routePluginAPI(value: string) {
  let path = value.replace(/^\//, '');
  if (path.startsWith('api/')) path = path.slice(4);
  let version = 'v1';
  if (/^v[12]\//.test(path)) {
    version = path.slice(0, 2);
    path = path.slice(3);
  }
  const pathname = path.split('?')[0]!,
    query = path.slice(pathname.length);
  // Preserve the fixed endpoints' wire contracts, including file responses and
  // raw discovery arrays. Only views absent from the Go source aliases use the
  // existing native endpoint and its envelope adapter.
  if (pathname === 'plugin' || pathname === 'plugin/')
    return { path: '/plugins' + query, host: true };
  if (pathname.startsWith('plugin/')) {
    const parts = pathname.slice(7).split('/');
    if (['form', 'page'].includes(parts[0]!) && parts.length === 2)
      return { path: '/plugins/' + parts[1] + '/' + parts[0] + query, host: true };
    if (parts[0] === 'dashboard') {
      if (parts[1] === 'meta' && parts.length === 2)
        return { path: '/plugins/dashboard/meta' + query, host: true };
      if (parts.length === 2 || parts.length === 3)
        return {
          path:
            '/plugins/' +
            parts[1] +
            '/dashboard' +
            query +
            (query ? '&' : '?') +
            'key=' +
            encodeURIComponent(parts[2] || ''),
          host: true,
        };
    }
    if (parts[0] === 'file') {
      return { path: '/' + version + '/plugin/' + parts.join('/') + query, host: false };
    }
    if (['sidebar_nav', 'remotes', 'folders'].includes(parts[0]!))
      return { path: '/' + version + '/' + path, host: false };
    if (pathname === 'plugin/installed') return { path: '/plugins/installed' + query, host: true };
    // Clone JS/CSS identities are derived centrally in the code package.
    // Runtime-computed source or peer URLs remain exactly what the caller sent.
    if (parts.length === 1) return { path: '/plugins/' + parts[0] + query, host: true };
    return { path: '/' + version + '/plugin/' + parts.join('/') + query, host: false };
  }
  if (pathname === 'user/config/PluginOrder')
    return { path: '/' + version + '/' + path, host: false };
  if (pathname.startsWith('plugins/'))
    return { path: '/' + path, host: !/^plugins\/[^/]+\/api\//.test(pathname) };
  if (
    [
      'system/runscheduler',
      'system/runscheduler2',
      'system/progress',
      'dashboard/schedule',
    ].includes(pathname)
  )
    return { path: '/' + version + '/' + path, host: true };
  return { path: '/' + path, host: true };
}
function resolvePluginRequest(config: AxiosRequestConfig) {
  const origin = globalThis.location?.origin || 'http://localhost';
  const defaultBase = apiURL('/');
  const raw = axios.getUri({
    ...config,
    baseURL: config.baseURL ?? defaultBase,
    params: undefined,
  });
  let target: URL;
  try {
    target = new URL(raw, origin);
  } catch {
    throw new PluginRemoteError('插件接口地址无效');
  }
  if (target.origin !== origin || target.username || target.password)
    throw new PluginRemoteError('插件接口地址无效：仅支持同源地址');
  const apiRoot = defaultBase.slice(0, -1);
  if (target.pathname === apiRoot || target.pathname.startsWith(apiRoot + '/')) {
    const mapped = routePluginAPI(target.pathname.slice(apiRoot.length) + target.search);
    return { ...mapped, url: apiRoot + mapped.path, baseURL: '' };
  }
  // An explicit same-origin absolute URL or baseURL outside /api retains its
  // actual address. Axios handles path normalization, params and fragments.
  return {
    path: target.pathname + target.search,
    url: target.pathname + target.search,
    baseURL: '',
    host: false,
  };
}
export function remotePluginAPIPath(
  pluginID: string,
  instance: string,
  path: string,
  _method: string,
) {
  if (!validPluginInstance(pluginID) || !validPluginInstance(instance))
    throw new PluginRemoteError('插件实例无效');
  return resolvePluginRequest({ url: path }).path;
}
function configureClient(api: AxiosInstance, changed?: () => void | Promise<void>) {
  api.interceptors.request.use((config) => {
    const mapped = resolvePluginRequest(config);
    if (!config.signal) {
      const controller = new AbortController();
      config.signal = controller.signal;
      activeRequests.add(controller);
      controller.signal.addEventListener('abort', () => activeRequests.delete(controller), {
        once: true,
      });
    }
    config.url = mapped.url;
    config.baseURL = mapped.baseURL;
    (config as RoutedRequest)._pluginHostEnvelope = mapped.host;
    // The fixed account bearer is represented by the existing HttpOnly cookie.
    // Use Axios' real fetch adapter so redirects can be rejected before any
    // submitted body or credentials could be forwarded to another origin.
    config.withCredentials = 'same-origin' as unknown as boolean;
    config.fetchOptions = { ...config.fetchOptions, redirect: 'error' };
    return config;
  });
  api.interceptors.response.use(
    (response) => {
      cleanupRequest(response.config);
      let result = response.data;
      if (
        (response.config as RoutedRequest)._pluginHostEnvelope &&
        result &&
        typeof result === 'object' &&
        !Array.isArray(result) &&
        Object.hasOwn(result, 'data') &&
        typeof result.request_id === 'string'
      )
        result = result.data;
      // The fixed Axios instance returns response.data even for200 success:false
      // or an error-valued business field. Callers retain their own decisions.
      if (response.config.method !== 'get' && result?.success === true && changed) {
        try {
          Promise.resolve(changed()).catch(() => {});
        } catch {
          /* acknowledgement remains valid */
        }
      }
      return result;
    },
    (error) => {
      cleanupRequest(error?.config);
      if (error?.response?.status === 401 || error?.response?.status === 403)
        globalThis.dispatchEvent?.(new Event('session-expired'));
      if (!error?.response) {
        if (error?.code === 'NETWORK_ERROR' || error?.code === 'ERR_NETWORK')
          return Promise.reject(
            new Error('Network connection failed, please check your network status'),
          );
        if (error?.code === 'ECONNABORTED' || error?.code === 'ETIMEDOUT')
          return Promise.reject(new Error('Request timeout, please try again later'));
        if (error?.name === 'AbortError') return Promise.reject(new Error('Request cancelled'));
        return Promise.reject(new Error(error?.message || 'Network error'));
      }
      return Promise.reject(error);
    },
  );
  const getUri = api.getUri.bind(api);
  api.getUri = (config = {}) => {
    const mapped = resolvePluginRequest({
      ...config,
      baseURL: config.baseURL ?? api.defaults.baseURL,
    });
    return getUri({ ...config, baseURL: mapped.baseURL, url: mapped.url });
  };
  return api as unknown as PluginRemoteAPI;
}
// Both global and component Axios APIs preserve the requested identity. Clone
// identifiers are rewritten centrally in the verified frontend files, including
// global API calls, CSS classes and asset names; runtime-built URLs stay intact.
export const pluginHostAPI = configureClient(
  axios.create({ baseURL: apiURL('/'), adapter: 'fetch' }),
);
if (typeof window !== 'undefined')
  (window as Window & { MoviePilotAPI?: PluginRemoteAPI }).MoviePilotAPI = pluginHostAPI;
export function createPluginRemoteAPI(
  pluginID: string,
  instance: string,
  signal: AbortSignal,
  onChanged?: () => void | Promise<void>,
) {
  if (!validPluginInstance(pluginID) || !validPluginInstance(instance))
    throw new PluginRemoteError('插件实例无效');
  return configureClient(axios.create({ ...pluginHostAPI.defaults, signal }), onChanged);
}

import { apiURL } from '../base-path.ts';
import { validPluginInstance } from './plugin-remote-contract.ts';

// This adapter binds a source API name to one installed instance. It is not
// an authorization boundary: registered routes and permissions stay on the
// server. Never accept an origin, another plugin prefix or traversal here.
export function remotePluginAPIPath(
  pluginID: string,
  instance: string,
  path: string,
  method: string,
) {
  if (!validPluginInstance(pluginID) || !validPluginInstance(instance))
    throw new Error('插件实例无效');
  const prefix = 'plugin/' + pluginID + '/';
  if (typeof path !== 'string' || !path.startsWith(prefix) ||
      !['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(method))
    throw new Error('插件接口地址无效');
  const endpoint = path.slice(prefix.length);
  if (!endpoint || endpoint.length > 16384 || /[\\\u0000-\u0020\u007f#]/.test(endpoint))
    throw new Error('插件接口地址无效');
  const pathname = endpoint.split('?')[0]!;
  try {
    // Reject malformed escapes, encoded separators and double-encoded paths
    // before URL/router normalization can move the request to another route.
    decodeURIComponent(endpoint);
    if (pathname.split('/').some((part) => {
      const decoded = decodeURIComponent(part);
      return !decoded || decoded === '.' || decoded === '..' ||
        /[/%?#\\\u0000-\u0020\u007f]/.test(decoded);
    })) throw new Error();
  } catch { throw new Error('插件接口地址无效'); }
  return '/plugins/' + encodeURIComponent(instance) + '/api/' + endpoint;
}

export class PluginRemoteError extends Error {
  constructor(
    message: string,
    public details?: unknown,
  ) {
    super(message);
  }
}

export function createPluginRemoteAPI(
  pluginID: string,
  instance: string,
  signal: AbortSignal,
  onChanged?: () => void | Promise<void>,
) {
  async function request(path: string, method: string, body?: unknown) {
    if (signal.aborted) throw new DOMException('插件请求已取消', 'AbortError');
    const target = remotePluginAPIPath(pluginID, instance, path, method);
    const response = await fetch(apiURL(target), {
      method,
      signal,
      credentials: 'same-origin',
      redirect: 'error',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (response.status === 401) globalThis.dispatchEvent?.(new Event('session-expired'));
    let result: any;
    try {
      result = await response.json();
    } catch {
      throw new PluginRemoteError('插件返回了无法读取的响应');
    }
    if (signal.aborted) throw new DOMException('插件请求已取消', 'AbortError');
    if (!response.ok || !result || typeof result !== 'object' || Array.isArray(result) || result.success === false || result.error) {
      const message =
        result?.message ||
        result?.error?.message ||
        (typeof result?.detail === 'string' ? result.detail : '插件操作失败');
      // Keep source error details; never turn an API refusal into success.
      throw new PluginRemoteError(message, result?.data ?? result?.detail);
    }
    if (method !== 'GET' && result?.success === true && onChanged) {
      // A confirmed source API write may change the applied sidebar state.
      // Refreshing its projection must not turn an acknowledged write into an
      // apparent failure and encourage the user to submit the operation again.
      try {
        Promise.resolve(onChanged()).catch(() => {});
      } catch {
        /* retain the actual operation result */
      }
    }
    return result;
  }
  return Object.freeze({
    get: (path: string) => request(path, 'GET'),
    post: (path: string, body: unknown) => request(path, 'POST', body),
    put: (path: string, body: unknown) => request(path, 'PUT', body),
    patch: (path: string, body: unknown) => request(path, 'PATCH', body),
    delete: (path: string) => request(path, 'DELETE'),
  });
}

import { apiURL } from '../base-path.ts';
import { hasOriginalPluginUI } from './plugin-remote-contract.ts';

const instanceID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$/;
const seedRescueRead = /^status(?:\?[^#]*)?$/;
const seedRescueWrite = /^(?:scan|jobs\/[a-fA-F0-9]{40}\/(?:retry|ignore|download))$/;

function brushFlowPath(endpoint: string, method: string) {
  const [path] = endpoint.split('?');
  if (!path) return false;
  const parts = path.split('/');
  if (method === 'GET' && endpoint === 'status') return true;
  if (method === 'POST' && /^(?:settings|tasks)$/.test(endpoint)) return true;
  if (parts[0] !== 'tasks' || !parts[1]) return false;
  try {
    const id = decodeURIComponent(parts[1]);
    if (id === '.' || id === '..' || /[/?#\\\u0000-\u001f]/.test(id)) return false;
  } catch {
    return false;
  }
  if (method === 'GET') return parts.length === 2 && !endpoint.includes('#');
  if (endpoint.includes('?')) return false;
  if (method === 'PUT' || method === 'DELETE') return parts.length === 2;
  return method === 'POST' && parts.length === 3 && /^(?:state|run|check|clear)$/.test(parts[2]!);
}

// Original Vue plugins receive an Axios-shaped API object. Bind it to this
// installed instance; a component cannot select another plugin or API origin.
export function remotePluginAPIPath(
  pluginID: string,
  instance: string,
  path: string,
  method: string,
) {
  if (!hasOriginalPluginUI(pluginID) || !instanceID.test(instance))
    throw new Error('此插件尚未提供原版 Vue 界面');
  const prefix = 'plugin/' + pluginID + '/';
  if (!path.startsWith(prefix)) throw new Error('插件接口地址无效');
  const endpoint = path.slice(prefix.length);
  const allowed =
    pluginID === 'BrushFlowLanduo'
      ? brushFlowPath(endpoint, method)
      : (method === 'GET' ? seedRescueRead : method === 'POST' ? seedRescueWrite : null)?.test(
          endpoint,
        );
  if (!allowed || /[\\\u0000-\u001f]/.test(endpoint)) throw new Error('插件接口地址无效');
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
    const target = remotePluginAPIPath(pluginID, instance, path, method);
    const response = await fetch(apiURL(target), {
      method,
      signal,
      credentials: 'same-origin',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    let result: any;
    try {
      result = await response.json();
    } catch {
      throw new PluginRemoteError('插件返回了无法读取的响应');
    }
    if (response.status === 401) globalThis.dispatchEvent?.(new Event('session-expired'));
    if (!response.ok || result?.success === false || result?.error) {
      const message =
        result?.message ||
        result?.error?.message ||
        (typeof result?.detail === 'string' ? result.detail : '插件操作失败');
      // The original workbench uses details.confirmation_required to open its
      // download risk dialog. Preserve the complete source response data.
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
    delete: (path: string) => request(path, 'DELETE'),
  });
}

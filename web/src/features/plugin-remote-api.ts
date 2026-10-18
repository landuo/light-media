import { apiURL } from '../base-path.ts';

const instanceID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$/;
const seedRescueRead = /^status(?:\?[^#]*)?$/;
const seedRescueWrite = /^(?:scan|jobs\/[a-fA-F0-9]{40}\/(?:retry|ignore|download))$/;

// Original Vue plugins receive an Axios-shaped API object. Bind it to this
// installed instance; a component cannot select another plugin or API origin.
export function remotePluginAPIPath(
  pluginID: string,
  instance: string,
  path: string,
  method: string,
) {
  if (pluginID !== 'SeedRescue' || !instanceID.test(instance))
    throw new Error('此插件尚未提供原版 Vue 界面');
  const prefix = 'plugin/' + pluginID + '/';
  if (!path.startsWith(prefix)) throw new Error('插件接口地址无效');
  const endpoint = path.slice(prefix.length);
  const allowed = method === 'GET' ? seedRescueRead : method === 'POST' ? seedRescueWrite : null;
  if (!allowed?.test(endpoint) || /[\\\u0000-\u001f]/.test(endpoint))
    throw new Error('插件接口地址无效');
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

export function createPluginRemoteAPI(pluginID: string, instance: string, signal: AbortSignal) {
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
    return result;
  }
  return Object.freeze({
    get: (path: string) => request(path, 'GET'),
    post: (path: string, body: unknown) => request(path, 'POST', body),
  });
}

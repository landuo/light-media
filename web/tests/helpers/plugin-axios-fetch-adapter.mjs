// Controlled transport boundary for the actual Axios client. The application
// uses Axios1.9's built-in fetch adapter; these tests keep their owned fetch
// fixtures while exercising Axios transforms, methods, config and rejection.
import axios from 'axios';
import { pluginHostAPI } from '../../src/features/plugin-remote-api.ts';
pluginHostAPI.defaults.adapter = async (config) => {
  const response = await globalThis.fetch(axios.getUri(config), {
    method: config.method.toUpperCase(),
    headers: config.headers,
    body: config.data,
    signal: config.signal,
    credentials: 'same-origin',
    redirect: 'error',
  });
  const data =
    config.responseType === 'arraybuffer'
      ? await response.arrayBuffer()
      : config.responseType === 'blob'
        ? await response.blob()
        : await response.text();
  const result = {
    data,
    status: response.status,
    statusText: response.statusText || '',
    headers: new axios.AxiosHeaders(Object.fromEntries(response.headers || [])),
    config,
    request: {},
  };
  if (!config.validateStatus || config.validateStatus(response.status)) return result;
  throw new axios.AxiosError(
    'Request failed with status code ' + response.status,
    response.status >= 500 ? 'ERR_BAD_RESPONSE' : 'ERR_BAD_REQUEST',
    config,
    {},
    result,
  );
};

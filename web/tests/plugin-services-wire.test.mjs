import test from 'node:test';
import assert from 'node:assert/strict';
import axios from 'axios';
import { createPluginRemoteAPI } from '../src/features/plugin-remote-api.ts';

test('all six service routes retain explicit v1/v2 paths and raw source response contracts', async () => {
  const client = createPluginRemoteAPI('Owned', 'Ownedcopy', new AbortController().signal);
  const requests = [];
  let payload;
  client.defaults.adapter = async (config) => {
    requests.push(config);
    return { config, status: 200, headers: {}, data: JSON.stringify(payload) };
  };
  for (const version of ['v1', 'v2']) {
    for (const route of [
      'dashboard/schedule',
      'dashboard/schedule2',
      'dashboard/schedule/Owned_tick/progress',
      'dashboard/schedule2/Owned_tick/progress',
      'system/runscheduler',
      'system/runscheduler2',
    ]) {
      const path = '/api/' + version + '/' + route;
      payload =
        route.endsWith('schedule') || route.endsWith('schedule2')
          ? version === 'v1'
            ? [{ id: 'Owned_tick' }]
            : { success: true, message: '', data: [{ id: 'Owned_tick' }] }
          : { success: true, message: null, message_i18n: null, data: {} };
      assert.deepEqual(await client.get(path), payload);
      assert.equal(requests.at(-1).url, path);
      assert.equal(requests.at(-1)._pluginHostEnvelope, false);
      for (const suffix of ['/', '//', '///']) {
        const slashed = path + suffix + '?jobid=unknown%zz&jobid=unknown;x=y';
        assert.deepEqual(await client.get(slashed), payload);
        assert.equal(requests.at(-1).url, slashed);
        assert.equal(requests.at(-1)._pluginHostEnvelope, false);
      }

      assert.equal(
        client.getUri({ url: path + '/?jobid=a%2Fb&jobid=&token=owned-fixture' }),
        path + '/?jobid=a%2Fb&jobid=&token=owned-fixture',
      );
    }
  }
  assert.equal(
    client.getUri({ url: 'dashboard/schedule2/Owned_tick/progress' }),
    '/api/v1/dashboard/schedule2/Owned_tick/progress',
  );
  assert.equal(
    client.getUri({ url: 'dashboard/schedule2', baseURL: 'http://localhost/api/v2/' }),
    '/api/v2/dashboard/schedule2',
  );
});

test('service wire preserves source failure bodies and never applies host data unwrapping', async () => {
  const client = createPluginRemoteAPI('Owned', 'Ownedcopy', new AbortController().signal);
  let payload = { data: { source: true }, request_id: 'owned-source-extension' },
    status = 200;
  client.defaults.adapter = async (config) => {
    const response = {
      config,
      status,
      headers: {},
      data: typeof payload === 'string' ? payload : JSON.stringify(payload),
    };
    if (status >= 400)
      throw new axios.AxiosError('Owned HTTP failure', 'ERR_BAD_RESPONSE', config, {}, response);
    return response;
  };
  assert.deepEqual(
    await client.get('v2/dashboard/schedule'),
    payload,
    'even a host-shaped payload stays raw on the source boundary',
  );
  payload = { success: false, message: '命令不能为空！', message_i18n: '命令不能为空！', data: {} };
  assert.deepEqual(await client.get('v1/system/runscheduler?jobid='), payload);
  for (const sample of [
    [
      422,
      {
        detail: [{ type: 'missing', loc: ['query', 'jobid'], msg: 'Field required', input: null }],
      },
    ],
    [422, { success: false, message: 'Field required', data: {} }],
    [500, 'Internal Server Error'],
  ]) {
    [status, payload] = sample;
    await assert.rejects(client.get('v2/system/runscheduler'), (error) => {
      assert.equal(error.response.status, status);
      assert.deepEqual(error.response.data, payload);
      return true;
    });
  }
});

test('native services and unrelated system/progress keep their established host adapters', async () => {
  const client = createPluginRemoteAPI('Owned', 'Ownedcopy', new AbortController().signal);
  const requests = [];
  client.defaults.adapter = async (config) => {
    requests.push(config);
    return {
      config,
      status: 200,
      headers: {},
      data: JSON.stringify({ data: { value: 37 }, request_id: 'owned' }),
    };
  };
  assert.deepEqual(await client.get('plugins/services'), { value: 37 });
  assert.deepEqual(await client.get('v2/system/progress'), { value: 37 });
  assert.equal(requests[0].url, '/api/plugins/services');
  assert.equal(requests[1].url, '/api/v2/system/progress');
  assert.equal(requests[0]._pluginHostEnvelope, true);
  assert.equal(requests[1]._pluginHostEnvelope, true);
});

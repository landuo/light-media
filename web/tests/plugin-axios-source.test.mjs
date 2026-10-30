import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import axios from 'axios';
import {
  createPluginRemoteAPI,
  pluginHostAPI,
  remotePluginAPIPath,
} from '../src/features/plugin-remote-api.ts';
const oracle = JSON.parse(
  readFileSync(new URL('../../testdata/standard-api/frontend-axios-oracle.json', import.meta.url)),
);
for (const c of oracle.cases)
  test('fixed actual Axios: ' + c.name, async () => {
    const calls = [];
    const client = createPluginRemoteAPI('Source', 'Sourcecopy', new AbortController().signal);
    client.defaults.adapter = async (config) => {
      calls.push({
        url: axios.getUri(config),
        method: config.method,
        body: config.data ?? null,
        timeout: config.timeout,
        header: config.headers.get('X-Source') ?? null,
      });
      const response = {
        data: c.response ?? '{"success":true}',
        status: c.status || 200,
        statusText: '',
        headers: new axios.AxiosHeaders({ 'Content-Type': c.contentType || 'application/json' }),
        config,
        request: {},
      };
      if (!config.validateStatus || config.validateStatus(response.status)) return response;
      throw new axios.AxiosError(
        'Request failed with status code ' + response.status,
        'ERR_BAD_RESPONSE',
        config,
        {},
        response,
      );
    };
    const config = { url: c.path, ...c.options };
    let output;
    try {
      let value;
      if (c.verb === 'call') value = await client(c.path, { method: 'get' });
      else if (c.verb === 'request') value = await client.request({ ...config, data: c.data });
      else if (c.verb === 'transform')
        value = await client.request({
          ...config,
          method: 'post',
          data: c.data,
          transformRequest: [() => 'literal raw'],
          transformResponse: [(body) => ({ transformed: body })],
        });
      else if (c.verb === 'post') value = await client.post(c.path, c.data, c.options);
      else value = await client[c.verb](c.path, c.options);
      output = { state: 'fulfilled', value };
    } catch (e) {
      output = {
        state: 'rejected',
        name: e.name,
        message: e.message,
        status: e.response?.status,
        data: e.response?.data,
      };
    }
    const { calls: sourceCalls, ...expected } = c.expected;
    assert.deepEqual(output, expected);
    assert.equal(calls[0].method, sourceCalls[0].method);
    assert.equal(calls[0].body, sourceCalls[0].body);
    assert.equal(calls[0].timeout, sourceCalls[0].timeout);
    const expectedPath = client.getUri({ url: c.path, ...c.options });
    assert.equal(calls[0].url, expectedPath);
    assert.equal(typeof client, 'function');
    assert.equal(typeof client.request, 'function');
    assert.equal(typeof client.defaults, 'object');
    assert.equal(typeof client.interceptors, 'object');
  });
test('native envelope adaptation keeps business false and explicit peer API unbound', async () => {
  const client = createPluginRemoteAPI('Source', 'Sourcecopy', new AbortController().signal);
  const seen = [];
  client.defaults.adapter = async (config) => {
    seen.push(config.url);
    return {
      data: JSON.stringify({ data: { success: false, message: 'empty' }, request_id: 'owned' }),
      status: 200,
      headers: {},
      config,
      request: {},
    };
  };
  assert.deepEqual(await client.post('plugins/services/run', {}), {
    success: false,
    message: 'empty',
  });
  assert.equal(
    remotePluginAPIPath('Source', 'Sourcecopy', 'plugin/Peer/state', 'GET'),
    '/v1/plugin/Peer/state',
  );
  assert.equal(pluginHostAPI.getUri({ url: 'plugin/Peer/state' }), '/api/v1/plugin/Peer/state');
  for (const url of ['https://elsewhere.invalid/x', '//elsewhere.invalid/x'])
    await assert.rejects(client.get(url), /接口地址无效/);
});

test('same-origin absolute URLs and baseURL retain source aliases; external origins never reach transport', async () => {
  const api = createPluginRemoteAPI('Source', 'Sourcecopy', new AbortController().signal);
  assert.equal(
    api.getUri({ url: 'http://localhost/api/v2/plugin/Source/state' }),
    '/api/v2/plugin/Source/state',
  );
  assert.equal(
    api.getUri({ url: 'plugin/Source/state', baseURL: 'http://localhost/api/v2/' }),
    '/api/v2/plugin/Source/state',
  );
  assert.equal(
    api.getUri({ url: 'plugin/file/Source/front/file.bin' }),
    '/api/v1/plugin/file/Source/front/file.bin',
  );
  assert.equal(
    api.getUri({ url: 'plugin/remotes?token=moviepilot' }),
    '/api/v1/plugin/remotes?token=moviepilot',
  );
  assert.equal(api.getUri({ url: 'plugin/sidebar_nav' }), '/api/v1/plugin/sidebar_nav');
  assert.equal(api.getUri({ url: 'healthz', baseURL: 'http://localhost/' }), '/healthz');
  let calls = 0;
  api.defaults.adapter = async () => {
    calls++;
    throw Error('unexpected');
  };
  for (const config of [
    { url: 'https://remote.invalid/api/plugin/Source/state' },
    { url: 'plugin/Source/state', baseURL: 'https://remote.invalid/api/' },
  ])
    await assert.rejects(api.request(config), /仅支持同源地址/);
  assert.equal(calls, 0);
});

test('fixed optimizer preserves explicit signals and aborts only automatically controlled global requests', async () => {
  const { abortPluginAPIRequests } = await import('../src/features/plugin-remote-api.ts');
  const explicit = new AbortController(),
    client = createPluginRemoteAPI('Source', 'Sourcecopy', explicit.signal);
  let own, global;
  const adapter = async (config) => {
    if (config.signal === explicit.signal) own = config.signal;
    else global = config.signal;
    return { data: '{}', status: 200, headers: {}, config };
  };
  client.defaults.adapter = adapter;
  const previous = pluginHostAPI.defaults.adapter;
  pluginHostAPI.defaults.adapter = async (config) => {
    global = config.signal;
    return new Promise(() => {});
  };
  try {
    await client.get('plugin/Source/state');
    void pluginHostAPI.get('plugin/Peer/state');
    await new Promise(setImmediate);
    abortPluginAPIRequests();
    assert.equal(own, explicit.signal);
    assert.equal(own.aborted, false);
    assert.equal(global.aborted, true);
  } finally {
    pluginHostAPI.defaults.adapter = previous;
  }
});

test('existing native list/config adaptation retains fixed management payloads rather than treating names as custom APIs',()=>{
 assert.equal(pluginHostAPI.getUri({url:'plugin/installed'}),'/api/plugins/installed');
 assert.equal(pluginHostAPI.getUri({url:'plugin/'}),'/api/plugins');
 const owner=createPluginRemoteAPI('Source','Sourcecopy',new AbortController().signal);
 assert.equal(owner.getUri({url:'plugin/Source'}),'/api/plugins/Source');
 assert.equal(owner.getUri({url:'plugin/form/Source'}),'/api/plugins/Source/form');
 assert.equal(owner.getUri({url:'plugin/dashboard/Source/stats'}),'/api/plugins/Source/dashboard?key=stats');
});

test('fixed existing folder and user-order aliases retain their raw wire; dynamic source URLs are never rebound', async()=>{
 const api=createPluginRemoteAPI('Source','Sourcecopy',new AbortController().signal);
 for(const [path,expected] of [['plugin/folders','/api/v1/plugin/folders'],['plugin/folders/Owned/plugins','/api/v1/plugin/folders/Owned/plugins'],['/api/v2/plugin/folders','/api/v2/plugin/folders'],['user/config/PluginOrder','/api/v1/user/config/PluginOrder']])assert.equal(api.getUri({url:path}),expected);
 const seen=[];api.defaults.adapter=async config=>{seen.push(config.url);return{data:'{"success":false,"data":{"message":"ordinary"}}',status:200,headers:{},config}};
 const runtime=['Sour','ce'].join('');assert.equal((await api.get('plugin/'+runtime+'/state')).success,false);assert.equal(seen[0],'/api/v1/plugin/Source/state');
});

test('source raw folder properties named data/request_id are not mistaken for a native host envelope',async()=>{
 const api=createPluginRemoteAPI('Source','Sourcecopy',new AbortController().signal),body={data:{plugins:['Source']},request_id:'authored-folder-property'};
 api.defaults.adapter=async config=>({data:JSON.stringify(body),status:200,headers:{},config});
 assert.deepEqual(await api.get('plugin/folders'),body);
});

test('statistics and ratings preserve actual Axios v1/v2 wire and method-sensitive root adapters',async()=>{
 const client=createPluginRemoteAPI('Source','Sourcecopy',new AbortController().signal);
 const calls=[];
 const source={success:true,message:'',data:{Owned:23},request_id:'authored-not-a-host-envelope'};
 client.defaults.adapter=async config=>{
  calls.push({url:axios.getUri(config),method:config.method,body:config.data??null});
  const native=config.url.startsWith('/api/plugins/');
  return {data:JSON.stringify(native?{data:source,request_id:'owned-host'}:source),status:200,headers:{},config};
 };
 for(const version of ['v1','v2'])for(const path of ['statistic','rating','rating/Owned']){
  for(const method of ['GET','POST','HEAD','OPTIONS','PATCH','PUT','DELETE']){
   const before=calls.length;
   const value=await client.request({url:`/api/${version}/plugin/${path}`,method,data:['POST','PUT','PATCH'].includes(method)?{rating:4.5}:undefined,params:{plugin_ids:'Owned,Other'}});
   assert.deepEqual(value,source,'source data/request_id fields must not be unwrapped');
   const native=!path.includes('/')&&['PUT','DELETE'].includes(method);
   const expected=`/api/${native?'plugins':version+'/plugin'}/${path}?plugin_ids=Owned,Other`;
   assert.equal(calls.length,before+1);
   assert.equal(calls.at(-1).url,expected);
   assert.equal(calls.at(-1).method,method.toLowerCase());
   assert.equal(calls.at(-1).body,['POST','PUT','PATCH'].includes(method)?'{"rating":4.5}':null);
   assert.equal(remotePluginAPIPath('Source','Sourcecopy',`/${version}/plugin/${path}`,method),expected.split('?')[0].replace(/^\/api/,''));
  }
 }
 assert.deepEqual(await client.get('/plugins/statistic'),source,'explicit native extension retains native envelope adaptation');
 assert.equal(calls.at(-1).url,'/api/plugins/statistic');
});

test('statistics/rating trailing slashes retain raw versioned paths and do not eagerly follow source redirects',async()=>{
 const client=createPluginRemoteAPI('Source','Sourcecopy',new AbortController().signal),calls=[];
 const payload={data:{Owned:23},request_id:'source-authored'};
 client.defaults.adapter=async config=>{
  calls.push(config);
  return {data:JSON.stringify(payload),status:200,headers:{},config};
 };
 for(const version of ['v1','v2'])for(const route of ['statistic','rating','rating/Owned'])for(const suffix of ['/','//','///']){
  for(const method of ['GET','HEAD','POST','OPTIONS','PATCH',...(route.includes('/')?['PUT','DELETE']:[])]){
   const path=`/api/${version}/plugin/${route}${suffix}?plugin_ids=A;B&plugin_ids=%GG`;
   assert.deepEqual(await client.request({url:path,method}),payload);
   assert.equal(calls.at(-1).url,path,'preserve the caller path/query; backend owns307');
   assert.equal(calls.at(-1)._pluginHostEnvelope,false);
   assert.equal(calls.at(-1).method,method.toLowerCase());
  }
 }
 // Only existing zero-slash generic root operations are claimed native here.
 for(const version of ['v1','v2'])for(const root of ['statistic','rating'])for(const method of ['PUT','DELETE']){
  assert.equal(remotePluginAPIPath('Source','Sourcecopy',`/${version}/plugin/${root}`,method),`/plugins/${root}`);
 }
});

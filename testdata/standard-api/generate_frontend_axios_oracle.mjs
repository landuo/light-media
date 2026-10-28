// Actual fixed Axios1.9 client initialization/interceptors, controlled transport.
import fs from 'node:fs';import crypto from 'node:crypto';import {createRequire} from 'node:module';
const targetRequire=createRequire(new URL('../../web/package.json',import.meta.url)),ts=targetRequire('typescript'),sourceRequire=createRequire('/Users/accidia/projects/MoviePilot-Frontend/package.json'),axios=sourceRequire('axios');
const root='/Users/accidia/projects/MoviePilot-Frontend/src/',apiSource=fs.readFileSync(root+'api/index.ts','utf8'),optimizerSource=fs.readFileSync(root+'utils/requestOptimizer.ts','utf8');
const optModule={exports:{}};new Function('exports','console',ts.transpileModule(optimizerSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(optModule.exports,{log(){}});
const body=apiSource.replace(/^import .*$/gm,'').replace(/declare global\s*\{[\s\S]*?\n\}/,'').replace('import.meta.env.VITE_API_BASE_URL',JSON.stringify('/api/v1/')).replace('export default api','return api');
const account={token:'synthetic-replay-only',logout(){}},offline={setAppOffline(){},resetConsecutiveErrors(){},recordNetworkError(){}};
const api=new Function('axios','window','initializeRequestOptimizer','useAuthStore','useGlobalOfflineStatus','router',body)(axios,{},optModule.exports.initializeRequestOptimizer,()=>account,()=>offline,{push(){}});
const cases=[
 {name:'same-origin-host-scheduler',path:'system/runscheduler',verb:'get',options:{params:{jobid:'owned root'}}},
 {name:'same-origin-web-message-leading-slash',path:'/message/web?text=%2Fmissing',verb:'post'},
 {name:'fixed-file-alias',path:'plugin/file/Source/frontend/assets/remoteEntry.js',verb:'get',response:'ordinary file',contentType:'text/javascript'},
 {name:'fixed-remotes-alias',path:'plugin/remotes?token=moviepilot',verb:'get',response:'[{"id":"Source"}]'},
 {name:'same-origin-absolute',path:'http://localhost/api/v2/plugin/Source/state',verb:'get'},
 {name:'same-origin-baseURL',path:'plugin/Source/state',verb:'get',options:{baseURL:'http://localhost/api/v2/'}},
 {name:'same-origin-root-baseURL',path:'healthz',verb:'get',options:{baseURL:'http://localhost/'}},
 {name:'explicit-other-plugin',path:'plugin/Peer/state',verb:'get'},
 {name:'callable-client',path:'plugin/Source/state',verb:'call'},
 {name:'request-config-and-transform',path:'plugin/Source/change',verb:'request',data:{count:2},options:{method:'post',params:{tags:['a','b']},headers:{'X-Source':'yes'},timeout:75}},
 {name:'HTTP200-success-false-resolves',path:'plugin/Source/noop',verb:'get',response:'{"success":false,"message":"owned no-op"}'},
 {name:'HTTP200-error-field-resolves',path:'plugin/Source/object',verb:'get',response:'{"error":{"message":"ordinary field"}}'},
 {name:'default-invalid-JSON-is-text',path:'plugin/Source/text',verb:'get',response:'not JSON',contentType:'application/json'},
 {name:'default-HTML-is-text',path:'plugin/Source/html',verb:'get',response:'<p>ordinary</p>',contentType:'text/html'},
 {name:'explicit-text-does-not-JSON-parse',path:'plugin/Source/text',verb:'get',response:'{"n":1}',options:{responseType:'text'}},
 {name:'bodyless204-empty-string',path:'plugin/Source/empty',verb:'delete',status:204,response:''},
 {name:'HTTP503-rejects-Axios-error',path:'plugin/Source/fail',verb:'get',status:503,response:'{"message":"owned failure"}'},
 {name:'request-custom-transforms',path:'plugin/Source/raw',verb:'transform',data:{private:'owned'},response:'owned response'}
];
for(const c of cases){const calls=[];api.defaults.adapter=async config=>{
 const response={data:c.response??'{"success":true}',status:c.status||200,statusText:'',headers:new axios.AxiosHeaders({'Content-Type':c.contentType||'application/json'}),config,request:{}};
 calls.push({url:axios.getUri(config),method:config.method,body:config.data??null,headers:{'X-Source':config.headers.get('X-Source')??null,'Content-Type':config.headers.get('Content-Type')??null},timeout:config.timeout,signal:!!config.signal,bearer_attached:config.headers.has('Authorization')});
 if(!config.validateStatus||config.validateStatus(response.status))return response;
 throw new axios.AxiosError('Request failed with status code '+response.status,'ERR_BAD_RESPONSE',config,{},response);
 };
 const config={url:c.path,...c.options};
 try{let value;if(c.verb==='call')value=await api(c.path,{method:'get'});else if(c.verb==='request')value=await api.request({...config,data:c.data});else if(c.verb==='transform')value=await api.request({...config,method:'post',data:c.data,transformRequest:[()=> 'literal raw'],transformResponse:[body=>({transformed:body})]});else if(c.verb==='post')value=await api.post(c.path,c.data,c.options);else value=await api[c.verb](c.path,c.options);c.expected={state:'fulfilled',value,calls}}catch(e){c.expected={state:'rejected',name:e.name,message:e.message,status:e.response?.status,data:e.response?.data,calls}}}
const sourcePackage=sourceRequire('axios/package.json');const output={fixed_frontend:'2829e3b08214348f05ff2f5beae644882eccad58',axios_version:sourcePackage.version,kind:'fixed api/index initialization + actual requestOptimizer and actual fixed Axios with controlled adapter; no external requests',sources:Object.fromEntries([['api/index.ts',apiSource],['utils/requestOptimizer.ts',optimizerSource]].map(([p,s])=>[p,crypto.createHash('sha256').update(s).digest('hex')])),surface:{callable:typeof api==='function',request:typeof api.request,defaults:typeof api.defaults,interceptors:typeof api.interceptors},cases};fs.writeFileSync(new URL('./frontend-axios-oracle.json',import.meta.url),JSON.stringify(output,null,2)+'\n');console.log('fixed actual Axios:',cases.length,'cases; version',sourcePackage.version);

// Owned acceptance component only; this authenticates no external service.
let scope;
export const init = value => { scope=value; const href=new URL('./ownedapi35fixture-ui.css',import.meta.url).href;if(!document.querySelector('link[data-owned-api35-style="'+href+'"]')){const link=document.createElement('link');link.rel='stylesheet';link.href=href;link.dataset.ownedApi35Style=href;document.head.appendChild(link)} };
export const get = async name => {
  if(!['./Page','./Config'].includes(name))throw Error('unknown owned API35 component');
  const vue=(await scope.vue[Object.keys(scope.vue)[0]].get())();
  const version='__OWNED_API35_VERSION__';
  return ()=>({
    props:['api','initialConfig'],emits:['save','close'],
    setup(props,{emit}){
      const result=vue.ref(''),error=vue.ref(''),busy=vue.ref(false),autoRecord=vue.ref(sessionStorage.getItem('owned-api35-auto-record')||'');
      const record=value=>{autoRecord.value=value;sessionStorage.setItem('owned-api35-auto-record',value)};
      const originalSource=['OwnedAPI','35Fixture'].join('');
      const path=endpoint=>'plugin/OwnedAPI35Fixture/'+endpoint;
      async function run(action){if(busy.value)return;busy.value=true;error.value='';try{result.value=await action()}catch(e){error.value=e.message}finally{busy.value=false}}
      const digest=async bytes=>Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),x=>x.toString(16).padStart(2,'0')).join('');
      const bytes=new Uint8Array([255,0,1,254,10]);
      const button=(label,action)=>vue.h('button',{type:'button',class:'v-btn v-btn--variant-tonal ma-2 pa-3',disabled:busy.value,onClick:()=>run(action)},label);
      return ()=>vue.h('div',{'data-owned-api35':version,'data-owned-api35-class':'OwnedAPI35Fixture',class:'ownedapi35fixture-scope'},name==='./Config'?[
        vue.h('pre',JSON.stringify(props.initialConfig)),button('保存当前配置',async()=>{emit('save',props.initialConfig);return '已提交配置'})
      ]:[
        button('读取实际状态',async()=>JSON.stringify(await props.api.get(path(version==='2.0'?'updated':'state')))),
        button('Axios callable及request',async()=>{const state=path(version==='2.0'?'updated':'state');return JSON.stringify({call:await props.api(state,{method:'get',params:{callable:'yes'}}),request:await props.api.request({url:state,method:'get',params:{request:'yes'},timeout:5000,headers:{'X-Owned':'axios'}})})}),
        button('同源绝对地址及baseURL',async()=>{const state=path(version==='2.0'?'updated':'state');return JSON.stringify({absolute:await props.api.get(location.origin+'/api/v2/'+state),base:await props.api.get(state,{baseURL:location.origin+'/api/v1/'})})}),
        button('显式其他分身接口',async()=>JSON.stringify(await props.api.get(version==='2.0'?'plugin/'+originalSource+'copy/state':'plugin/'+originalSource+'/updated'))),
        button('全局MoviePilotAPI',async()=>JSON.stringify(await window.MoviePilotAPI.get(path(version==='2.0'?'updated':'state')))),
        button('普通宿主消息历史',async()=>JSON.stringify(await props.api.get('/message/web',{params:{page:1,size:20}}))),
        button('普通宿主调度拒绝',async()=>JSON.stringify(await props.api.get('system/runscheduler',{params:{jobid:'owned-unknown-no-such-job'}}))),
        button('HTTP200业务字段原样返回',async()=>JSON.stringify({noop:await props.api.post(path('noop'),{owned:true}),object:await props.api.get(path('object'))})),
        button('Axios读取原版文件流',async()=>{const data=await props.api.get('plugin/file/OwnedAPI35Fixture/owned-api35-fixture',{responseType:'arraybuffer'});return `原版file流 ${data.byteLength} 字节 SHA256=${await digest(data)}`}),
        button('运行时拼接指向源',async()=>JSON.stringify(await props.api.get('plugin/'+originalSource+'/updated'))),
        button('读取原版分组及排序',async()=>JSON.stringify({folders:await props.api.get('plugin/folders'),order:await props.api.get('user/config/PluginOrder')})),
        button('启动全局慢请求后导航',async()=>{record('pending');try{const data=await window.MoviePilotAPI.get(path('slow'),{timeout:20000});record('fulfilled '+JSON.stringify(data));return autoRecord.value}catch(e){record('rejected '+e.name+': '+e.message);throw e}}),
        vue.h('pre',{'data-owned-api35-auto-record':'',role:'status'},autoRecord.value),
        button('拒绝跨源凭据',async()=>{try{await props.api.get('https://owned-cross-origin.invalid/blocked')}catch(e){return `已拒绝跨源 ${e.message}`}throw Error('cross-origin unexpectedly accepted')}),
        button('调用匿名接口',async()=>JSON.stringify(await props.api.get(path('anonymous')))),
        button('读取二进制下载',async()=>{const blob=await props.api.get(path('binary'),{responseType:'blob'});const data=await blob.arrayBuffer();return `下载 ${blob.size} 字节 ${blob.type} SHA256=${await digest(data)}`}),
        button('上传二进制并回读',async()=>{const data=await props.api.post(path('binary'),bytes,{responseType:'arraybuffer'});return `上传回读 ${data.byteLength} 字节 SHA256=${await digest(data)} 一致=${await digest(data)===await digest(bytes)}`}),
        button('签名原文回显',async()=>{const raw='{ "signature": "测试" }\n';const value=await props.api.patch(path('change/browser?name=browser'),new Blob([raw],{type:'application/octet-stream'}),{headers:{'X-Correlation-ID':'owned-browser'}});const returned=Uint8Array.from(atob(value.request.body),x=>x.charCodeAt(0));return `原文回显 ${returned.byteLength} 字节 一致=${await digest(returned)===await digest(new TextEncoder().encode(raw))}`}),
        button('调用空响应',async()=>{await props.api.delete(path('empty'));return '204 空响应通过'}),
        button('验证失败响应',async()=>{await props.api.get(path('failure'));return '不应到达'}),
        vue.h('a',{href:'/api/v1/plugin/file/OwnedAPI35Fixture/owned-api35-fixture',target:'_blank',rel:'noopener'},'读取源插件代码文件流'),
        vue.h('pre',{'data-owned-api35-result':'',role:'status'},result.value),
        error.value?vue.h('p',{role:'alert'},error.value):null
      ]);
    }
  });
};

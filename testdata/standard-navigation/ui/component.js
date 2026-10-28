const css=new URL('./fixture.css',import.meta.url).href;
if(!document.querySelector('link[data-owned-ui3133]')){
 const link=document.createElement('link');link.rel='stylesheet';link.href=css;link.dataset.ownedUi3133='true';document.head.appendChild(link);
}
export const component=(v,u,name)=>({
 name:'OwnedUI3133'+name.slice(2),props:['initialConfig','api','navKey','pluginId','config','allowRefresh'],emits:['save','close'],
 setup(p,{emit}){
  const C=Object.fromEntries(['VCard','VCardTitle','VCardText','VBtn','VTextField','VSwitch','VTabs','VTab','VAlert','VChip','VDialog','VDataTable'].map(n=>[n,v.resolveComponent(n)]));
  const theme=u.useTheme(),display=u.useDisplay();
  const marker=v.ref(p.initialConfig?.marker||''),enabled=v.ref(p.initialConfig?.enabled!==false),tab=v.ref('form'),output=v.ref({}),error=v.ref(''),dialog=v.ref(false),busy=v.ref(false);
  const read=async(fail=false)=>{busy.value=true;error.value='';try{output.value=await p.api.get('plugin/OwnedFrontendFixture/status',{params:fail?{fail:true}:{}})}catch(e){error.value=e.message}finally{busy.value=false}};
  const btn=(text,fn,props={})=>v.h(C.VBtn,{onClick:fn,variant:'tonal',class:'ma-1',...props},()=>text);
  v.onMounted(()=>{if(name!=='./Config')void read()});
  return()=>v.h(C.VCard,{'data-owned-ui3133':name,class:'owned-ui3133'},()=>[
   v.h(C.VCardTitle,{},()=>`Owned shared ${name.slice(2)} __OWNED_UI_VERSION__`),
   v.h(C.VCardText,{},()=>[
    v.h(C.VChip,{color:'primary',variant:'tonal',class:'mb-3','data-owned-shared':'true'},()=>`Vue ${v.version} / Vuetify ${u.version} / ${theme.current.value.colors.primary} / ${display.name.value}`),
    name==='./Config'?[
     v.h(C.VTabs,{modelValue:tab.value,'onUpdate:modelValue':x=>tab.value=x},()=>[v.h(C.VTab,{value:'form'},()=> '测试表单'),v.h(C.VTab,{value:'preview'},()=> '配置预览')]),
     tab.value==='form'?v.h('div',{class:'owned-fields'},[
      v.h(C.VTextField,{label:'Owned shared marker',modelValue:marker.value,'onUpdate:modelValue':x=>marker.value=x}),
      v.h(C.VSwitch,{label:'Owned shared enabled',modelValue:enabled.value,'onUpdate:modelValue':x=>enabled.value=x}),
      btn('保存共享表单',()=>emit('save',{...p.initialConfig,marker:marker.value,enabled:enabled.value}))
     ]):v.h('pre',JSON.stringify({...p.initialConfig,marker:marker.value,enabled:enabled.value},null,2))
    ]:[
     v.h('p',{'data-owned-observed':true},`${output.value.version||'pending'} / ${output.value.marker||''} / PID ${output.value.nonce||''}`),
     v.h('p',{'data-owned-props':true},`pluginId=${p.pluginId||''} navKey=${p.navKey||''} dashboard=${p.config?.key||''} refresh=${String(p.allowRefresh)}`),
     btn('读取共享API',()=>read(),{loading:busy.value}),btn('触发共享API失败',()=>read(true)),btn('打开共享弹窗',()=>dialog.value=true),
     v.h(C.VDataTable,{headers:[{title:'实例数据',key:'marker'},{title:'版本',key:'version'},{title:'初始化次数',key:'init_count'}],items:output.value.version?[output.value]:[],hideDefaultFooter:true,density:'compact'}),
     v.h(C.VDialog,{modelValue:dialog.value,'onUpdate:modelValue':x=>dialog.value=x,width:360},()=>v.h(C.VCard,{},()=>[v.h(C.VCardTitle,{},()=> 'Owned Vuetify dialog'),v.h(C.VCardText,{},()=>`Shared theme ${theme.current.value.colors.primary}`),btn('关闭共享弹窗',()=>dialog.value=false)]))
    ],
    error.value?v.h(C.VAlert,{type:'error',class:'mt-3',role:'alert'},()=>error.value):null
   ])
  ]);
 }
});

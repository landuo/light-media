let scope;
export const init=s=>{scope=s};
export const get=async name=>{
 if(!['./Config','./Page','./Dashboard','./AppPage'].includes(name))throw Error('Unknown owned UI3133 expose '+name);
 const v=(await scope.vue[Object.keys(scope.vue)[0]].get())();
 const u=(await scope.vuetify[Object.keys(scope.vuetify)[0]].get())();
 const module=await import('./component.js');
 return()=>module.component(v,u,name);
};

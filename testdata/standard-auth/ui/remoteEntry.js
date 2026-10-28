let scope;
export const init = value => { scope = value; };
export const get = async name => {
  if (name !== './AuthPage') throw new Error('Unknown owned auth component');
  const vue = (await scope.vue[Object.keys(scope.vue)[0]].get())();
  const module = await import('./auth.js');
  return () => module.component(vue);
};

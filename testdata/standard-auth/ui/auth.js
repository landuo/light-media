// Synthetic identity proof for the owned SDK fixture only. This published test
// key authenticates no real account/service and is never a production provider.
export const component = vue => ({
  props: ['api', 'provider'],
  emits: ['authenticated', 'error', 'close'],
  setup(props, { emit }) {
    const busy = vue.ref(false);
    async function authenticate(valid) {
      if (busy.value) return;
      busy.value = true;
      try {
        const encoder = new TextEncoder();
        const key = await crypto.subtle.importKey('raw', encoder.encode('owned-auth-proof-key'), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
        const nonce = crypto.randomUUID();
        const signature = await crypto.subtle.sign('HMAC', key, encoder.encode(nonce));
        const proof = valid ? Array.from(new Uint8Array(signature), x => x.toString(16).padStart(2, '0')).join('') : '00';
        // The original compiled frontend can retain its source API prefix when
        // copied into a clone; the parent binds that prefix to this instance.
        const result = await props.api.post('plugin/OwnedAuth/authenticate', { nonce, proof, provider_id: props.provider.id });
        if (typeof result.ticket !== 'string') throw new Error('测试提供方没有返回票据');
        emit('authenticated', { ticket: result.ticket });
      } catch (error) { emit('error', error); }
      finally { busy.value = false; }
    }
    return () => vue.h('div', { 'data-owned-auth': 'ready' }, [
      vue.h('p', '独立测试身份，仅用于本地验证。'),
      vue.h('button', { type: 'button', disabled: busy.value, onClick: () => authenticate(false) }, '验证拒绝路径'),
      vue.h('button', { type: 'button', disabled: busy.value, onClick: () => authenticate(true) }, '验证测试身份'),
    ]);
  },
});

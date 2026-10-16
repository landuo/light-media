export function newAPIToken(crypto: Pick<Crypto, 'getRandomValues'>): string {
  // The original alphabet has 64 characters: modulo introduces no bias.
  const alphabet = '0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-_';
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (value) => alphabet[value % alphabet.length]).join('');
}

export async function copyAPIToken(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      /* HTTP NAS pages use the fallback. */
    }
  }
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const field = document.createElement('textarea');
  field.value = value;
  field.setAttribute('readonly', '');
  field.style.position = 'fixed';
  field.style.top = '-9999px';
  document.body.append(field);
  try {
    field.select();
    if (!document.execCommand('copy')) throw new Error('复制失败，请手动复制令牌');
  } finally {
    field.remove();
    previous?.focus({ preventScroll: true });
  }
}

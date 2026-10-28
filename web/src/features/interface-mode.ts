import { computed, ref } from 'vue';

export type InterfaceMode = 'auto' | 'desktop' | 'app';
export const interfaceModeKey = 'LIGHT_MEDIA_UI_MODE';
export function readInterfaceMode(storage?: Pick<Storage, 'getItem'>): InterfaceMode {
  try {
    const value = storage?.getItem(interfaceModeKey);
    return value === 'app' || value === 'desktop' ? value : 'auto';
  } catch {
    return 'auto';
  }
}
export function isAppInterface(mode: InterfaceMode, pwa: boolean, width: number) {
  return mode === 'app' || (mode === 'auto' && pwa && width < 1280);
}
export function isPluginPWAEnvironment(input: {
  standalone?: boolean;
  referrer?: string;
  userAgent?: string;
  touch?: boolean;
  width: number;
  serviceWorker?: boolean;
}) {
  return !!(
    input.standalone ||
    input.serviceWorker ||
    input.referrer?.includes('android-app://') ||
    /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini|Mobile|mobile|CriOS/i.test(
      input.userAgent || '',
    ) ||
    input.touch ||
    input.width < 768
  );
}
function environment(serviceWorker = false) {
  if (typeof window === 'undefined') return false;
  return isPluginPWAEnvironment({
    standalone:
      window.matchMedia?.('(display-mode: standalone)').matches ||
      (navigator as Navigator & { standalone?: boolean }).standalone,
    referrer: document.referrer,
    userAgent: navigator.userAgent,
    touch: 'ontouchstart' in window || navigator.maxTouchPoints > 0,
    width: window.innerWidth,
    serviceWorker,
  });
}
function initialMode() {
  try {
    return typeof window === 'undefined'
      ? ('auto' as const)
      : readInterfaceMode(globalThis.localStorage);
  } catch {
    return 'auto' as const;
  }
}
export const interfaceMode = ref<InterfaceMode>(initialMode());
const viewport = ref(typeof window === 'undefined' ? 1280 : window.innerWidth);
const pwa = ref(environment());
export const appInterface = computed(() =>
  isAppInterface(interfaceMode.value, pwa.value, viewport.value),
);
export function setInterfaceMode(value: InterfaceMode, storage?: Pick<Storage, 'setItem'>) {
  if (!['auto', 'desktop', 'app'].includes(value)) throw new Error('界面布局无效');
  interfaceMode.value = value;
  try {
    (storage || globalThis.localStorage)?.setItem(interfaceModeKey, value);
  } catch {
    /* layout remains usable if browser storage is unavailable */
  }
}
export function observeInterfaceEnvironment() {
  let active = true;
  const resize = () => {
    viewport.value = window.innerWidth;
  };
  resize();
  pwa.value = environment();
  window.addEventListener('resize', resize);
  if (navigator.serviceWorker?.getRegistrations) {
    navigator.serviceWorker
      .getRegistrations()
      .then((registrations) => {
        if (active) pwa.value = environment(registrations.length > 0);
      })
      .catch(() => {});
  }
  return () => {
    active = false;
    window.removeEventListener('resize', resize);
  };
}

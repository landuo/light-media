import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime.js';
import zhCN from 'dayjs/locale/zh-cn.js';
import { apiURL } from '../base-path.ts';

// MoviePilot's pinned frontend uses Day.js 1.11.13 + default relativeTime + zh-cn.
dayjs.extend(relativeTime);

// One authenticated App owns one consumer of the server's shared FIFO. This is
// deliberately not a broadcast, persisted history, or a notification read API.
export const SYSTEM_MESSAGE_INITIAL_DELAY = 3000;
export const SYSTEM_MESSAGE_BACKGROUND_DELAY = 5000;
export const SYSTEM_MESSAGE_RECONNECT_DELAYS = [3000, 4500, 6750] as const;
export const SYSTEM_MESSAGE_LIMIT = 100;
export const SYSTEM_MESSAGE_FRAME_LIMIT = 256 * 1024;

export interface SystemMessage {
  id: number;
  type: string;
  title: string | null;
  text: string | null;
  date: string | null;
  read: boolean;
}
export interface SystemMessageState {
  items: SystemMessage[];
  hasNew: boolean;
}
export type SystemMessageSource = Pick<
  EventSource,
  'readyState' | 'onopen' | 'onerror' | 'onmessage' | 'close'
>;
interface LifecycleTarget {
  addEventListener(name: string, listener: () => void): void;
  removeEventListener(name: string, listener: () => void): void;
}
export interface SystemMessageEnvironment {
  document: LifecycleTarget & { readonly hidden: boolean };
  window: LifecycleTarget;
  setTimeout(callback: () => void, delay: number): number;
  clearTimeout(timer: number): void;
  createSource(url: string): SystemMessageSource;
}

export function parseSystemMessage(data: unknown): Omit<SystemMessage, 'id' | 'read'> | null {
  if (typeof data !== 'string' || !data || data.length > SYSTEM_MESSAGE_FRAME_LIMIT) return null;
  if (new TextEncoder().encode(data).byteLength > SYSTEM_MESSAGE_FRAME_LIMIT) return null;
  try {
    const value: unknown = JSON.parse(data);
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
    const row = value as Record<string, unknown>;
    if (typeof row.type !== 'string') return null;
    for (const key of ['title', 'text', 'date']) {
      if (row[key] !== undefined && row[key] !== null && typeof row[key] !== 'string') return null;
    }
    // Never retain or interpret note, URLs, components, or caller-supplied IDs.
    return {
      type: row.type,
      title: typeof row.title === 'string' ? row.title : null,
      text: typeof row.text === 'string' ? row.text : null,
      date: typeof row.date === 'string' ? row.date : null,
    };
  } catch {
    return null;
  }
}

export function systemMessageIcon(type: string): string {
  return type === 'user' ? 'mdi-account-alert' : type === 'plugin' ? 'mdi-robot' : 'mdi-laptop';
}

export function systemMessageRelativeTime(date: string | null, at = Date.now()): string {
  if (!date) return '';
  const parsed = dayjs(date);
  // Explicit display hardening: unlike the upstream formatter, malformed dates
  // stay blank rather than displaying a fabricated relative time.
  if (!parsed.isValid()) return '';
  // Per-instance zh-cn prevents unrelated locale changes from altering the bell.
  // Passing the injectable current time preserves the original fromNow behavior
  // for normal dates without an ICU-specific formatter or fixed month lengths.
  return parsed.locale(zhCN).from(dayjs(at));
}

export function createSystemMessageFeed(
  changed: (state: SystemMessageState) => void,
  environment?: SystemMessageEnvironment,
) {
  const env: SystemMessageEnvironment = environment || {
    document,
    window,
    setTimeout: (callback, delay) => window.setTimeout(callback, delay),
    clearTimeout: (timer) => window.clearTimeout(timer),
    // Same-origin EventSource carries the session cookie. No token URL or new
    // credentials are used; native CONNECTING retries remain browser-managed.
    createSource: (url) => new EventSource(url),
  };
  let state: SystemMessageState = { items: [], hasNew: false };
  let authenticated = false;
  let destroyed = false;
  let generation = 0;
  let nextID = 0;
  let initialReady = false;
  let source: SystemMessageSource | null = null;
  let initialTimer: number | null = null;
  let reconnectTimer: number | null = null;
  let backgroundTimer: number | null = null;

  function publish(next: SystemMessageState) {
    state = next;
    changed(state);
  }
  function clearTimer(timer: number | null) {
    if (timer !== null) env.clearTimeout(timer);
  }
  function closeSource() {
    if (!source) return;
    const old = source;
    source = null;
    old.onopen = old.onerror = old.onmessage = null;
    old.close();
  }
  function scheduleReconnect(attempt: number) {
    if (
      !authenticated ||
      destroyed ||
      env.document.hidden ||
      attempt > SYSTEM_MESSAGE_RECONNECT_DELAYS.length
    )
      return;
    clearTimer(reconnectTimer);
    const epoch = generation;
    reconnectTimer = env.setTimeout(
      () => {
        reconnectTimer = null;
        if (epoch === generation) connect(attempt);
      },
      SYSTEM_MESSAGE_RECONNECT_DELAYS[attempt - 1]!,
    );
  }
  function connect(attempt = 0) {
    if (!authenticated || destroyed || env.document.hidden || !initialReady || source) return;
    clearTimer(reconnectTimer);
    reconnectTimer = null;
    const epoch = generation;
    let current: SystemMessageSource;
    try {
      current = env.createSource(apiURL('/system/message'));
    } catch {
      scheduleReconnect(attempt + 1);
      return;
    }
    source = current;
    const isCurrent = () =>
      authenticated && !destroyed && epoch === generation && current === source;
    current.onopen = () => {
      if (isCurrent()) attempt = 0;
    };
    current.onerror = () => {
      if (!isCurrent()) return;
      // CONNECTING is EventSource's native retry state, not a reason to create
      // a second consumer. Only CLOSED starts the three manual backoff steps.
      if (current.readyState === 2) {
        closeSource();
        scheduleReconnect(attempt + 1);
      }
    };
    current.onmessage = (event) => {
      if (!isCurrent()) return;
      const message = parseSystemMessage(event.data);
      if (!message) return;
      publish({
        items: [{ ...message, id: ++nextID, read: false }, ...state.items].slice(
          0,
          SYSTEM_MESSAGE_LIMIT,
        ),
        hasNew: true,
      });
    };
  }
  function visibilityChanged() {
    if (!authenticated || destroyed) return;
    clearTimer(backgroundTimer);
    backgroundTimer = null;
    if (env.document.hidden) {
      clearTimer(reconnectTimer);
      reconnectTimer = null;
      const epoch = generation;
      backgroundTimer = env.setTimeout(() => {
        backgroundTimer = null;
        if (epoch === generation && env.document.hidden) closeSource();
      }, SYSTEM_MESSAGE_BACKGROUND_DELAY);
    } else if (!source || source.readyState === 2) {
      closeSource();
      connect();
    }
  }
  function setAuthenticated(value: boolean) {
    if (destroyed || value === authenticated) return;
    authenticated = value;
    generation++;
    if (value) {
      initialReady = false;
      env.document.addEventListener('visibilitychange', visibilityChanged);
      env.window.addEventListener('beforeunload', destroy);
      const epoch = generation;
      initialTimer = env.setTimeout(() => {
        initialTimer = null;
        if (epoch !== generation) return;
        initialReady = true;
        connect();
      }, SYSTEM_MESSAGE_INITIAL_DELAY);
    } else {
      clearTimer(initialTimer);
      clearTimer(reconnectTimer);
      clearTimer(backgroundTimer);
      initialTimer = reconnectTimer = backgroundTimer = null;
      initialReady = false;
      closeSource();
      env.document.removeEventListener('visibilitychange', visibilityChanged);
      env.window.removeEventListener('beforeunload', destroy);
      publish({ items: [], hasNew: false });
    }
  }
  function destroy() {
    if (destroyed) return;
    setAuthenticated(false);
    destroyed = true;
  }
  return {
    setAuthenticated,
    destroy,
    markAllRead() {
      publish({
        items: state.items.map((item) => ({ ...item, read: true })),
        hasNew: false,
      });
    },
  };
}

export const pluginLogLevels = ['TRACE', 'DEBUG', 'INFO', 'WARNING', 'ERROR', 'CRITICAL'];
export const pluginLogLineLimit = 600;

export type PluginLogEntry = {
  id: string;
  raw: string;
  level: string;
  timestamp: string;
  timestampMs: number | null;
  secondKey: string;
  secondDisplay: string;
  timeDisplay: string;
  source: string;
  message: string;
};
export type PluginLogGroup = {
  id: string;
  level: string;
  secondKey: string;
  secondDisplay: string;
  items: PluginLogEntry[];
  lastTimestampMs: number | null;
};

export function normalizePluginLogLevel(level: string): string {
  const value = level.trim().replace(/:$/, '').toUpperCase();
  return value === 'WARN' ? 'WARNING' : value === 'FATAL' ? 'CRITICAL' : value;
}

export function pluginLogEntry(value: any, instance: string): PluginLogEntry | null {
  if (
    !value ||
    typeof value !== 'object' ||
    Array.isArray(value) ||
    value.plugin_instance_id !== instance ||
    typeof value.id !== 'string' ||
    !value.id ||
    value.id.length > 100 ||
    typeof value.message !== 'string' ||
    value.message.length > 16384
  )
    return null;
  const clean = (text: any) =>
    String(text ?? '')
      .replace(/\u001B\[[0-9;]*m/g, '')
      .replace(/\r/g, '')
      .trimEnd();
  const level = normalizePluginLogLevel(clean(value.level));
  const timestamp = clean(value.time);
  const ms = Date.parse(timestamp);
  const date = Number.isFinite(ms) ? new Date(ms) : null;
  const pad = (n: number, width = 2) => String(n).padStart(width, '0');
  const secondKey = date
    ? `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
    : '';
  const timeDisplay = date ? secondKey.slice(11) + ',' + pad(date.getMilliseconds(), 3) : '';
  const source = clean(value.module);
  const code = clean(value.error_code);
  const message = clean(value.message) + (code ? ` [${code}]` : '');
  return {
    id: value.id,
    raw: `${level}: ${timestamp} ${source} - ${message}`,
    level,
    timestamp,
    timestampMs: date ? ms : null,
    secondKey,
    secondDisplay: secondKey.replaceAll('-', '/'),
    timeDisplay,
    source,
    message,
  };
}

export function appendPluginLogs(existing: PluginLogEntry[], incoming: PluginLogEntry[]) {
  const ids = new Set(existing.map((item) => item.id));
  const accepted: PluginLogEntry[] = [];
  for (const item of incoming) {
    if (ids.has(item.id)) continue;
    ids.add(item.id);
    accepted.push(item);
  }
  return {
    entries: [...existing, ...accepted].slice(-pluginLogLineLimit),
    count: accepted.length,
  };
}

export function groupPluginLogs(entries: PluginLogEntry[]): PluginLogGroup[] {
  const groups: PluginLogGroup[] = [];
  for (const item of entries) {
    const last = groups.at(-1);
    if (
      last &&
      last.secondKey &&
      item.secondKey &&
      last.secondKey === item.secondKey &&
      last.level === item.level &&
      (last.lastTimestampMs === null ||
        item.timestampMs === null ||
        item.timestampMs - last.lastTimestampMs <= 100)
    ) {
      last.items.push(item);
      if (item.timestampMs !== null) last.lastTimestampMs = item.timestampMs;
    } else {
      groups.push({
        id: `${item.secondKey || 'log'}-${item.level || 'plain'}-${item.id}`,
        level: item.level,
        secondKey: item.secondKey,
        secondDisplay: item.secondDisplay,
        items: [item],
        lastTimestampMs: item.timestampMs,
      });
    }
  }
  return groups;
}

export function filterPluginLogGroups(
  groups: PluginLogGroup[],
  level: string,
  query: string | null,
): PluginLogGroup[] {
  const needle = (query ?? '').trim().toLowerCase();
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter(
        (item) =>
          (level === 'ALL' || item.level === level) &&
          (!needle ||
            [item.raw, item.level, '', item.timestamp, item.source, item.message]
              .join(' ')
              .toLowerCase()
              .includes(needle)),
      ),
    }))
    .filter((group) => group.items.length > 0);
}

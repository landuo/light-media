export const pluginLogLevels = ['TRACE', 'DEBUG', 'INFO', 'WARNING', 'ERROR', 'CRITICAL'];
export const pluginLogLineLimit = 600;

export type PluginLogEntry = {
  id: string;
  raw: string;
  appName?: string;
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
            [item.raw, item.level, item.appName || '', item.timestamp, item.source, item.message]
              .join(' ')
              .toLowerCase()
              .includes(needle)),
      ),
    }))
    .filter((group) => group.items.length > 0);
}

// Fixed LoggingView parser is needed for physical continuation lines. The
// leading structured host record already supplies its own trusted metadata.
type ParsedLog = {
  level: string;
  appName: string;
  timestamp: string;
  source: string;
  message: string;
  structured: boolean;
};
const ANSI_PATTERN = /\u001B\[[0-9;]*m/g;
const TIMESTAMP_PATTERN = /\d{4}-\d{2}-\d{2}(?:[ T]\d{2}:\d{2}(?::\d{2})?(?:,\d{3})?)?/;
function stripAnsi(text: string) {
  return text.replace(ANSI_PATTERN, '');
}

function extractTimestamp(text: string) {
  return text.match(TIMESTAMP_PATTERN)?.[0] ?? '';
}

function getTimestampMs(timestamp: string) {
  if (!timestamp) {
    return null;
  }

  const normalizedTimestamp = timestamp.replace(' ', 'T').replace(',', '.');
  const parsedTimestamp = Date.parse(normalizedTimestamp);

  return Number.isNaN(parsedTimestamp) ? null : parsedTimestamp;
}

function getSecondKey(timestamp: string) {
  if (!timestamp) {
    return '';
  }

  return timestamp.replace('T', ' ').slice(0, 19);
}

function getSecondDisplay(secondKey: string) {
  return secondKey ? secondKey.replaceAll('-', '/') : '';
}

function getTimeDisplay(timestamp: string) {
  if (!timestamp) {
    return '';
  }

  return timestamp.split(' ').at(-1) ?? timestamp;
}

function extractMessage(text: string) {
  return (
    text
      .split(/\s+-\s+/)
      .slice(1)
      .join(' - ') || text
  );
}

function parsePythonStyleLog(raw: string): ParsedLog | null {
  const match = raw.match(/^([A-Za-z]+):\s+(.*)$/);
  if (!match) {
    return null;
  }

  const [, rawLevel, remainder] = match;
  const level = normalizePluginLogLevel(rawLevel);
  if (!pluginLogLevels.includes(level)) {
    return null;
  }

  const body = remainder.trim();
  const bodyMatch = body.match(
    /^(?:\[([^\]]+)\]\s+)?(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:,\d{3})?)\s+([^\s]+)\s+-\s+(.*)$/,
  );

  return {
    level,
    appName: bodyMatch?.[1] ?? '',
    timestamp: bodyMatch?.[2] ?? extractTimestamp(body),
    source: bodyMatch?.[3] ?? '',
    message: bodyMatch?.[4] ?? extractMessage(body),
    structured: Boolean(bodyMatch),
  };
}

function parseBracketStyleLog(raw: string): ParsedLog | null {
  const match = raw.match(/^【([^】]+)】\s*(.*)$/);
  if (!match) {
    return null;
  }

  const [, rawLevel, remainder] = match;
  const level = normalizePluginLogLevel(rawLevel);
  const body = remainder.trim();
  const bodyMatch = body.match(
    /^(?:\[([^\]]+)\]\s+)?(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:,\d{3})?)\s+([^\s]+)\s+-\s+(.*)$/,
  );

  return {
    level,
    appName: bodyMatch?.[1] ?? '',
    timestamp: bodyMatch?.[2] ?? extractTimestamp(body),
    source: bodyMatch?.[3] ?? '',
    message: bodyMatch?.[4] ?? extractMessage(body),
    structured: Boolean(bodyMatch),
  };
}

function parseTimestampFirstLog(raw: string): ParsedLog | null {
  const match = raw.match(
    new RegExp(`^(${TIMESTAMP_PATTERN.source})\\s+\\[?([A-Za-z]+)\\]?\\s*(.*)$`),
  );
  if (!match) {
    return null;
  }

  const [, timestamp, rawLevel, remainder] = match;
  const level = normalizePluginLogLevel(rawLevel);
  if (!pluginLogLevels.includes(level)) {
    return null;
  }

  const body = remainder.trim();
  const bodyMatch = body.match(/^(?:\[([^\]]+)\]\s+)?([^\s]+)\s+-\s+(.*)$/);

  return {
    level,
    appName: bodyMatch?.[1] ?? '',
    timestamp,
    source: bodyMatch?.[2] ?? '',
    message: bodyMatch?.[3] ?? extractMessage(body),
    structured: Boolean(bodyMatch),
  };
}

function parseInlineLevelLog(raw: string): ParsedLog | null {
  const match = raw.match(/^\[?([A-Za-z]+)\]?:?\s+(.*)$/);
  if (!match) {
    return null;
  }

  const [, rawLevel, remainder] = match;
  const level = normalizePluginLogLevel(rawLevel);
  if (!pluginLogLevels.includes(level)) {
    return null;
  }

  const body = remainder.trim();
  const timestamp = extractTimestamp(body);
  const bodyWithoutTimestamp = timestamp ? body.replace(timestamp, '').trim() : body;
  const bodyMatch = bodyWithoutTimestamp.match(/^(?:\[([^\]]+)\]\s+)?([^\s]+)\s+-\s+(.*)$/);

  return {
    level,
    appName: bodyMatch?.[1] ?? '',
    timestamp,
    source: bodyMatch?.[2] ?? '',
    message: bodyMatch?.[3] ?? extractMessage(bodyWithoutTimestamp),
    structured: Boolean(bodyMatch),
  };
}

function pluginLogContinuation(rawLine: string, id: string): PluginLogEntry {
  const raw = stripAnsi(rawLine).replace(/\r/g, '').trimEnd();
  const parsed =
    parsePythonStyleLog(raw) ??
    parseBracketStyleLog(raw) ??
    parseTimestampFirstLog(raw) ??
    parseInlineLevelLog(raw);
  const timestamp = parsed?.timestamp ?? extractTimestamp(raw);
  const secondKey = getSecondKey(timestamp);
  return {
    id,
    raw,
    appName: parsed?.appName ?? '',
    level: parsed?.level ?? '',
    timestamp,
    timestampMs: getTimestampMs(timestamp),
    secondKey,
    secondDisplay: getSecondDisplay(secondKey),
    timeDisplay: getTimeDisplay(timestamp),
    source: parsed?.source ?? '',
    message: parsed?.message ?? raw,
  };
}

// The pinned server/view operates on physical lines: subsequent lines of one
// logged message do not inherit the first line's severity or timestamp. Stable
// line IDs adapt that behavior to the host's existing resume/deduplication API.
export function pluginLogEntries(value: any, instance: string): PluginLogEntry[] {
  const record = pluginLogEntry(value, instance);
  if (!record) return [];
  if (typeof value.raw === 'string')
    return value.raw
      .split(/\r?\n/)
      .flatMap((line: string, index: number) =>
        line.trim()
          ? [pluginLogContinuation(line.trim(), index ? value.id + ':line:' + index : value.id)]
          : [],
      );
  const lines = value.message.split(/\r?\n/);
  const first = pluginLogEntry({ ...value, message: lines[0] }, instance)!;
  return [
    first,
    ...lines
      .slice(1)
      .flatMap((line: string, index: number) =>
        line.trim() ? [pluginLogContinuation(line.trim(), value.id + ':line:' + (index + 1))] : [],
      ),
  ];
}

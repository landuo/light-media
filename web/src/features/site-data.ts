export type SiteStats = {
  level?: string;
  joined_at?: string;
  uploaded?: number | null;
  downloaded?: number | null;
  ratio?: number | null;
  ratio_infinite?: boolean;
  bonus?: number | null;
  seeding?: number | null;
  seeding_bytes?: number | null;
  seeding_info?: [number, number][] | null;
  captured_at?: string;
};
export type SiteDay = { day: string; stats: SiteStats };

// The original dialog compares the last two daily observations, not two
// refreshes on the same day. Failed observations never become zero traffic.
export function siteDataDays(items: any[], latest?: SiteStats | null): SiteDay[] {
  const days = new Map<string, SiteStats>();
  function add(stats: SiteStats | null | undefined, day?: string) {
    if (!stats?.captured_at) return;
    const at = new Date(stats.captured_at);
    if (!Number.isFinite(at.getTime())) return;
    day ||= new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai' }).format(at);
    const previous = days.get(day);
    if (!previous || Date.parse(stats.captured_at) >= Date.parse(previous.captured_at!))
      days.set(day, stats);
  }
  for (const item of items) {
    if (item.connection?.status !== 'failed') add(item.stats, item.updated_day);
  }
  add(latest);
  return [...days].sort(([a], [b]) => a.localeCompare(b)).map(([day, stats]) => ({ day, stats }));
}

export function siteDataDelta(days: SiteDay[], key: keyof SiteStats): number | null {
  const latest = days.at(-1)?.stats;
  const previous = days.at(-2)?.stats;
  if (key === 'ratio' && (latest?.ratio_infinite || previous?.ratio_infinite)) return null;
  const value = latest?.[key];
  if (typeof value !== 'number' || !Number.isFinite(value)) return null;
  if (!previous) return value;
  const before = previous[key];
  return typeof before === 'number' && Number.isFinite(before) ? value - before : null;
}

export function siteDataDailySeries(days: SiteDay[], key: keyof SiteStats): (number | null)[] {
  return days.map((day, index) => {
    if (index === 0) return null;
    const previous = days[index - 1];
    const a = Date.parse(day.day + 'T00:00:00Z'),
      b = Date.parse(previous.day + 'T00:00:00Z');
    if (!Number.isFinite(a) || !Number.isFinite(b) || Math.round((a - b) / 86400000) !== 1)
      return null;
    const latestValue = day.stats[key],
      previousValue = previous.stats[key];
    if (typeof latestValue !== 'number' || typeof previousValue !== 'number') return null;
    return latestValue - previousValue;
  });
}

export function siteDataLatestDailyDelta(days: SiteDay[], key: keyof SiteStats): number | null {
  const series = siteDataDailySeries(days, key);
  return series.length ? series[series.length - 1] : null;
}

export function siteDataDeltaSpan(days: SiteDay[]): number | null {
  const latest = days.at(-1)?.day,
    previous = days.at(-2)?.day;
  if (!latest || !previous) return null;
  const a = Date.parse(latest + 'T00:00:00Z'),
    b = Date.parse(previous + 'T00:00:00Z');
  if (!Number.isFinite(a) || !Number.isFinite(b)) return null;
  return Math.round((a - b) / 86400000);
}

export function siteDataPreviousDay(days: SiteDay[]): string | null {
  return days.at(-2)?.day || null;
}

export function siteDataBytes(value: number, signed = false): string {
  let size = Math.abs(value),
    unit = 0;
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit++;
  }
  return `${value < 0 ? '-' : signed ? '+' : ''}${size.toFixed(2)} ${units[unit]}`;
}

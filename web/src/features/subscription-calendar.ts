export type CalendarView = 'month' | 'week' | 'list';
export function calendarDay(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}
function parsed(day: string) {
  const value = new Date(day + 'T12:00:00');
  if (
    !/^\d{4}-\d{2}-\d{2}$/.test(day) ||
    Number.isNaN(value.valueOf()) ||
    calendarDay(value) !== day
  )
    throw new Error('请选择有效的日历日期');
  return value;
}
export function calendarDates(anchor: string, view: CalendarView) {
  const start = parsed(anchor);
  if (view === 'list') {
    const first = new Date(start.getFullYear(), start.getMonth(), 1, 12);
    const count = new Date(start.getFullYear(), start.getMonth() + 1, 0, 12).getDate();
    return Array.from({ length: count }, (_, index) =>
      calendarDay(new Date(first.getFullYear(), first.getMonth(), index + 1, 12)),
    );
  }
  if (view === 'month') start.setDate(1);
  start.setDate(start.getDate() - ((start.getDay() + 6) % 7));
  return Array.from({ length: view === 'week' ? 7 : 42 }, (_, index) => {
    const day = new Date(start);
    day.setDate(day.getDate() + index);
    return calendarDay(day);
  });
}
export function moveCalendar(anchor: string, view: CalendarView, offset: number) {
  const day = parsed(anchor);
  if (view === 'week') day.setDate(day.getDate() + offset * 7);
  else {
    day.setDate(1);
    day.setMonth(day.getMonth() + offset);
  }
  return calendarDay(day);
}
export function calendarGroups(entries: Record<string, any>[]) {
  const groups = new Map<string, any>();
  for (const entry of entries) {
    const key = [entry.subscription_id, entry.date, entry.source].join(':');
    if (!groups.has(key)) groups.set(key, { ...entry, key, entries: [] });
    groups.get(key).entries.push(entry);
  }
  return [...groups.values()].sort(
    (a, b) => a.date.localeCompare(b.date) || a.title.localeCompare(b.title, 'zh-CN'),
  );
}
export function calendarEpisodes(entries: Record<string, any>[]) {
  const episodes = [
    ...new Set(
      entries
        .filter((entry) => entry.confirmed && entry.episode > 0)
        .map((entry) => Number(entry.episode)),
    ),
  ].sort((a, b) => a - b);
  if (!episodes.length) return entries[0]?.media_type === 'movie' ? '电影' : '更新';
  return episodes.map((episode) => 'E' + String(episode).padStart(2, '0')).join('、');
}

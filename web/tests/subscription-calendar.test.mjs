import test from 'node:test';
import assert from 'node:assert/strict';
import {
  calendarDates,
  moveCalendar,
  calendarGroups,
  calendarEpisodes,
} from '../src/features/subscription-calendar.ts';
test('calendar month and week begin on Monday and use actual adjacent dates', () => {
  const month = calendarDates('2026-10-15', 'month');
  assert.equal(month.length, 42);
  assert.equal(month[0], '2026-09-28');
  assert.equal(month.at(-1), '2026-11-08');
  assert.deepEqual(calendarDates('2026-10-18', 'week'), [
    '2026-10-12',
    '2026-10-13',
    '2026-10-14',
    '2026-10-15',
    '2026-10-16',
    '2026-10-17',
    '2026-10-18',
  ]);
  assert.equal(calendarDates('2028-02-20', 'list').length, 29);
  assert.throws(() => calendarDates('2026-02-30', 'month'));
});
test('month navigation does not skip February from a 31st date and week navigation crosses years', () => {
  assert.equal(moveCalendar('2026-01-31', 'month', 1), '2026-02-01');
  assert.equal(moveCalendar('2026-12-29', 'week', 1), '2027-01-05');
});
test('calendar groups real episode events without inventing episode numbers for predictions', () => {
  const events = [
    {
      subscription_id: 'tv',
      date: '2026-10-16',
      source: 'metadata',
      title: '节目',
      media_type: 'tv',
      confirmed: true,
      episode: 2,
    },
    {
      subscription_id: 'tv',
      date: '2026-10-16',
      source: 'metadata',
      title: '节目',
      media_type: 'tv',
      confirmed: true,
      episode: 1,
    },
    {
      subscription_id: 'tv',
      date: '2026-10-16',
      source: 'prediction',
      title: '节目',
      media_type: 'tv',
      confirmed: false,
      episode: 0,
    },
  ];
  const groups = calendarGroups(events);
  assert.equal(groups.length, 2);
  assert.equal(calendarEpisodes(groups[0].entries), 'E01、E02');
  assert.equal(calendarEpisodes(groups[1].entries), '更新');
});

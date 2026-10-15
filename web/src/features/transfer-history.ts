export const historyPageSizes = [25, 50, 100, 500, 1000] as const;

export interface HistoryRow {
  id: string;
  media: { title: string; media_type: string; year?: number; tmdb_id?: number };
  season: number;
  episodes: number[];
  source: string;
  target: string;
  mode: string;
  size: number;
  status: string;
  updated_at: string;
  error?: string;
  can_recognize?: boolean;
  [key: string]: any;
}
export interface HistoryViewState {
  search: string;
  page: number;
  pageSize: number;
  grouped: boolean;
}
export type HistorySort = 'title' | 'source' | 'mode' | 'size' | 'updated_at' | 'status';

export function historyTitle(row: HistoryRow): string {
  return (
    row.media?.title?.trim() ||
    row.source?.split('/').filter(Boolean).pop() ||
    row.target?.split('/').filter(Boolean).pop() ||
    '未识别'
  );
}

export function historyMediaType(row: HistoryRow): string {
  return row.media?.media_type === 'movie'
    ? '电影'
    : row.media?.media_type === 'tv'
      ? '电视剧'
      : '未识别';
}

export function historyViewState(hash: string): HistoryViewState {
  const params = new URLSearchParams(hash.split('?')[1] || '');
  const pageSize = Number(params.get('itemsPerPage'));
  const page = Number(params.get('currentPage'));
  return {
    search: params.get('search') || '',
    page: Number.isSafeInteger(page) && page > 0 && page <= 100000 ? page : 1,
    pageSize: historyPageSizes.some((value) => value === pageSize) ? pageSize : 50,
    grouped: params.get('grouped') === 'true',
  };
}

export function historyViewHash(state: HistoryViewState): string {
  const params = new URLSearchParams();
  if (state.search) params.set('search', state.search);
  params.set('itemsPerPage', String(state.pageSize));
  params.set('currentPage', String(state.page));
  if (state.grouped) params.set('grouped', 'true');
  return '#history?' + params;
}

export function historyQuery(state: HistoryViewState): string {
  const status = state.search === '成功' ? 'success' : state.search === '失败' ? 'failed' : '';
  return new URLSearchParams({
    q: status ? '' : state.search,
    status,
    page: String(state.page),
    page_size: String(state.pageSize),
  }).toString();
}

export function sortHistory(
  rows: HistoryRow[],
  key: HistorySort,
  descending: boolean,
): HistoryRow[] {
  const text = (a: string, b: string) =>
    a.localeCompare(b, 'zh-CN', { sensitivity: 'base', numeric: true });
  const result = [...rows].sort((a, b) => {
    if (key === 'title') {
      return (
        text(a.media.media_type || '', b.media.media_type || '') ||
        text(historyTitle(a), historyTitle(b)) ||
        a.season - b.season ||
        (a.episodes?.[0] || 0) - (b.episodes?.[0] || 0)
      );
    }
    if (key === 'size') return a.size - b.size;
    return text(String(a[key] || ''), String(b[key] || ''));
  });
  return descending ? result.reverse() : result;
}

export function historyGroups(rows: HistoryRow[]): { title: string; items: HistoryRow[] }[] {
  const groups = new Map<string, HistoryRow[]>();
  for (const row of rows) {
    const title = historyTitle(row);
    const group = groups.get(title) || [];
    group.push(row);
    groups.set(title, group);
  }
  return [...groups].map(([title, items]) => ({ title, items }));
}

export function historyPageButtons(page: number, total: number): number[] {
  const start = Math.max(1, Math.min(page - 3, total - 6));
  return Array.from({ length: Math.min(7, total) }, (_, index) => start + index);
}

export function transferQueueState(file: { status: string; job_state?: string }): string {
  if (['success', 'imported', 'already_imported', 'completed', 'succeeded'].includes(file.status))
    return 'completed';
  if (file.status === 'blocked') return 'failed';
  if (['cancelled', 'canceled'].includes(file.job_state || file.status)) return 'cancelled';
  if (file.job_state === 'failed') return 'failed';
  if (file.job_state === 'running' || file.status === 'importing') return 'running';
  if (['queued', 'retry_wait', 'waiting_paused'].includes(file.job_state || '')) return 'waiting';
  if (file.status === 'failed') return 'failed';
  if (['ready', 'pending', 'queued', 'waiting'].includes(file.status)) return 'waiting';
  return 'unknown';
}

export interface TransferQueueGroup {
  id: string;
  title: string;
  media?: { title?: string; year?: number; [key: string]: any };
  status?: string;
  error?: string;
  files: {
    id: string;
    source: string;
    job_id?: string;
    job_state?: string;
    error?: string;
    [key: string]: any;
  }[];
}

// Original queue tabs group independently submitted files by media title/year.
// Keep the job facts on every file: an error in one job must not label its peers.
export function groupTransferQueue(input: TransferQueueGroup[]): TransferQueueGroup[] {
  const groups = new Map<string, TransferQueueGroup>();
  for (const group of input) {
    const title = group.media?.title?.trim();
    for (const file of group.files || []) {
      const key = title
        ? 'media:' + JSON.stringify([title, group.media?.year || 0])
        : 'file:' + JSON.stringify([file.source || '', file.id || group.id]);
      let merged = groups.get(key);
      if (!merged) {
        merged = {
          id: key,
          title: title || file.source?.split('/').filter(Boolean).pop() || group.title || '未识别',
          media: group.media,
          files: [],
        };
        groups.set(key, merged);
      }
      const jobState = file.job_state || group.status || '';
      merged.files.push({
        ...file,
        job_id: file.job_id || group.id,
        job_state: jobState,
        error:
          file.error ||
          (transferQueueState({ status: file.status, job_state: jobState }) === 'failed'
            ? group.error || ''
            : ''),
      });
    }
  }
  return [...groups.values()];
}

export interface ExploreMedia {
  id: string | number;
  media_type: string;
  source?: string;
  source_id?: string | number;
  season?: number;
  [key: string]: unknown;
}
export interface ExplorePage {
  results: ExploreMedia[];
  has_more?: boolean;
  total_pages?: number;
}
export interface ExploreFeed {
  results: ExploreMedia[];
  page: number;
  generation: number;
  loading: boolean;
  loaded: boolean;
  hasMore: boolean;
  error: string;
}
export type ExploreLoadStatus = 'ok' | 'empty' | 'error';
export function createExploreFeed(): ExploreFeed {
  return {
    results: [],
    page: 0,
    generation: 0,
    loading: false,
    loaded: false,
    hasMore: true,
    error: '',
  };
}
export function resetExploreFeed(feed: ExploreFeed) {
  const generation = feed.generation + 1;
  Object.assign(feed, createExploreFeed(), { generation });
}
export function exploreMediaKey(item: ExploreMedia) {
  if (typeof item.plugin_media_key === 'string') return item.plugin_media_key;
  return [
    item.source || 'themoviedb',
    item.media_type,
    item.source_id ?? item.id,
    item.season ?? '',
  ].join('/');
}
export async function loadExplorePage(
  feed: ExploreFeed,
  request: (page: number) => Promise<ExplorePage>,
  formatError: (error: unknown) => string = (error) =>
    error instanceof Error ? error.message : '加载失败',
): Promise<ExploreLoadStatus> {
  if (feed.loading) return 'ok';
  if (!feed.hasMore) return 'empty';
  const generation = feed.generation,
    page = feed.page + 1;
  feed.loading = true;
  feed.error = '';
  try {
    const result = await request(page);
    if (generation !== feed.generation) return 'empty';
    const incoming = result.results || [],
      seen = new Set(feed.results.map(exploreMediaKey));
    feed.results = feed.results.concat(
      incoming.filter((item) => {
        const key = exploreMediaKey(item);
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      }),
    );
    feed.page = page;
    feed.loaded = true;
    feed.hasMore =
      result.has_more ??
      (incoming.length > 0 && (result.total_pages ? page < result.total_pages : true));
    return feed.hasMore ? 'ok' : 'empty';
  } catch (error) {
    if (generation !== feed.generation) return 'empty';
    feed.error = formatError(error);
    return 'error';
  } finally {
    if (generation === feed.generation) feed.loading = false;
  }
}

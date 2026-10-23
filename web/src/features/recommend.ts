import { loadExplorePage, type ExploreFeed, type ExplorePage } from './explore-feed.ts';

export type RecommendCategory = 'all' | 'movie' | 'tv' | 'anime' | 'rankings';
export interface RecommendSource {
  id: string;
  title: string;
  category: Exclude<RecommendCategory, 'all'>;
}
export const recommendCategories: { id: RecommendCategory; title: string; icon: string }[] = [
  { id: 'all', title: '全部', icon: 'mdi-filmstrip-box-multiple' },
  { id: 'movie', title: '电影', icon: 'mdi-movie' },
  { id: 'tv', title: '电视剧', icon: 'mdi-television-classic' },
  { id: 'anime', title: '动漫', icon: 'mdi-animation' },
  { id: 'rankings', title: '榜单', icon: 'mdi-trophy' },
];

export function recommendEnabled(sources: RecommendSource[], saved: Record<string, boolean> = {}) {
  return Object.fromEntries(sources.map((source) => [source.id, saved[source.id] !== false]));
}

export function recommendSourceVisible(
  source: RecommendSource,
  category: RecommendCategory,
  enabled: Record<string, boolean>,
) {
  return enabled[source.id] !== false && (category === 'all' || source.category === category);
}

export function recommendPath(id: string, page: number) {
  return '/catalog/recommend?' + new URLSearchParams({ id, page: String(page) });
}

export function recommendBrowseRoute(source: RecommendSource) {
  return (
    'recommend-browse/' +
    encodeURIComponent(source.id) +
    '?' +
    new URLSearchParams({ title: source.title })
  );
}

export function recommendBrowseSource(route: string) {
  const [path = '', query = ''] = route.split('?');
  let id = '';
  try {
    id = decodeURIComponent(path.split('/')[1] || '');
  } catch {
    // An invalid route must not be turned into a different source identifier.
  }
  return { id, title: new URLSearchParams(query).get('title') || '' };
}

// A shelf requests its first page once, including empty responses. Only its retry
// control may retry a failed request; hidden shelves never start work.
export async function ensureRecommendShelf(
  feed: ExploreFeed,
  visible: boolean,
  request: (page: number) => Promise<ExplorePage>,
  formatError?: (error: unknown) => string,
) {
  if (!visible || feed.loaded || feed.error) return;
  await loadExplorePage(feed, request, formatError);
}

export interface RecommendPreferenceState {
  enabled: Record<string, boolean>;
  saving: boolean;
  error: string;
}

export async function saveRecommendPreferences(
  state: RecommendPreferenceState,
  sources: RecommendSource[],
  draft: Record<string, boolean>,
  request: (enabled: Record<string, boolean>) => Promise<unknown>,
  formatError: (error: unknown) => string = (error) =>
    error instanceof Error ? error.message : '保存失败',
) {
  if (state.saving) return false;
  const enabled = recommendEnabled(sources, draft);
  state.saving = true;
  state.error = '';
  try {
    await request({ ...enabled });
    state.enabled = enabled;
    return true;
  } catch (error) {
    state.error = formatError(error);
    return false;
  } finally {
    state.saving = false;
  }
}

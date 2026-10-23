import { submitSeasonBatch, type SeasonSubscriptionRow } from './subscriptions.ts';

export type ExploreAction = 'search' | 'subscribe';
export type ExploreActionMedia = Record<string, any> & {
  id: number | string;
  media_type: string;
};
export type ExploreSite = { id: string; name: string };
export type ExploreMovieIntent = { action: 'create' | 'cancel'; id?: string };
export type ExploreSeasonRow = SeasonSubscriptionRow & {
  air_date: string;
  subscription_state: string;
};

let rememberedSites: string[] | undefined;
export function selectedExploreSites(available: ExploreSite[], defaults: unknown) {
  const enabled = new Set(available.map((site) => site.id));
  const selected = rememberedSites ?? (Array.isArray(defaults) ? defaults.map(String) : []);
  return [...new Set(selected)].filter((id) => enabled.has(id));
}
export function rememberExploreSites(selected: string[], available: ExploreSite[]) {
  const enabled = new Set(available.map((site) => site.id));
  rememberedSites = [...new Set(selected)].filter((id) => enabled.has(id));
}

export function tmdbExploreMedia(media: ExploreActionMedia | null | undefined) {
  if (
    !media ||
    (media.source && media.source !== 'themoviedb') ||
    !['movie', 'tv'].includes(media.media_type) ||
    !Number.isInteger(Number(media.id)) ||
    Number(media.id) <= 0 ||
    Number(media.id) > 2147483647
  )
    return null;
  return { ...media, id: Number(media.id), source: 'themoviedb' } as ExploreActionMedia;
}

export function exploreResolvePath(media: ExploreActionMedia) {
  if (
    !['douban', 'bangumi', 'anilist'].includes(media.source) ||
    !['movie', 'tv'].includes(media.media_type) ||
    !/^[1-9]\d*$/.test(String(media.source_id || ''))
  )
    throw new Error('影视来源或编号无效');
  return (
    '/catalog/explore/resolve?' +
    new URLSearchParams({
      source: media.source,
      kind: media.media_type,
      source_id: String(media.source_id),
    })
  );
}

export function enabledExploreSites(response: any): ExploreSite[] {
  const rows = Array.isArray(response) ? response : response?.sites;
  if (!Array.isArray(rows)) throw new Error('站点列表返回格式无效');
  const seen = new Set<string>();
  return rows.flatMap((row) => {
    const site = row.config || row;
    const id = String(site.id || '');
    if (!site.enabled || !id || /[,\r\n\x00]/.test(id) || seen.has(id)) return [];
    seen.add(id);
    return [{ id, name: String(site.name || id) }];
  });
}

export function exploreResourceRoute(
  media: ExploreActionMedia,
  selected: string[],
  available: ExploreSite[],
) {
  const resolved = tmdbExploreMedia(media);
  if (!resolved) throw new Error('请先确认影视身份');
  const enabled = new Set(available.map((site) => site.id));
  const sites = [...new Set(selected)].filter((id) => enabled.has(id));
  if (!sites.length) throw new Error('请至少选择一个已启用站点');
  return `resources/${resolved.media_type}/${resolved.id}?${new URLSearchParams({ sites: sites.join(',') })}`;
}

export function exploreSubscriptions(media: ExploreActionMedia, response: any): any[] {
  const rows = Array.isArray(response) ? response : response?.items;
  if (!Array.isArray(rows)) throw new Error('订阅列表返回格式无效');
  return rows.filter(
    (row) =>
      row.id &&
      !['cancelled', 'canceled'].includes(row.state) &&
      row.media?.media_type === media.media_type &&
      Number(row.media?.id) === Number(media.id),
  );
}

export function exploreMovieIntent(
  existing: any[],
  previous: ExploreMovieIntent | null = null,
): ExploreMovieIntent {
  return (
    previous || (existing[0]?.id ? { action: 'cancel', id: existing[0].id } : { action: 'create' })
  );
}

export function pendingExploreMovieMutation(intent: ExploreMovieIntent, existing: any[]) {
  if (intent.action === 'create') return existing.length ? null : 'create';
  return existing.some((row) => row.id === intent.id) ? 'cancel' : null;
}

export async function cancelExploreSubscriptions(
  ids: string[],
  existing: any[],
  cancel: (id: string) => Promise<any>,
  changed: (remaining: any[]) => void,
  describe: (error: unknown) => string,
) {
  let remaining = [...existing];
  const failures: string[] = [];
  for (const id of [...new Set(ids)]) {
    const subscription = remaining.find((row) => row.id === id);
    if (!subscription) continue;
    try {
      const result = await cancel(id);
      if (!result?.cancelled) throw new Error('取消订阅的结果未确认，请刷新后重试');
      remaining = remaining.filter((row) => row.id !== id);
      changed(remaining);
    } catch (cause) {
      failures.push(
        `${subscription.season ? '第 ' + subscription.season + ' 季：' : ''}${describe(cause)}`,
      );
    }
  }
  return { remaining, failures };
}

export function exploreSubscriptionBody(media: ExploreActionMedia) {
  const resolved = tmdbExploreMedia(media);
  if (!resolved) throw new Error('请先确认影视身份');
  return {
    media_type: resolved.media_type,
    tmdb_id: resolved.id,
    media: {
      id: resolved.id,
      media_type: resolved.media_type,
      title: resolved.title || '',
      year: resolved.year || 0,
    },
    target_scope: resolved.media_type === 'tv' ? 'season' : 'movie',
    season: 0,
    start_episode: resolved.media_type === 'tv' ? 1 : 0,
    episodes: [] as number[],
  };
}

export function exploreSeasonRows(media: ExploreActionMedia, subscriptions: any[]) {
  const seen = new Set<number>();
  return (Array.isArray(media.seasons) ? media.seasons : []).flatMap(
    (season: any): ExploreSeasonRow[] => {
      const number = Number(season.season_number);
      if (!Number.isInteger(number) || number <= 0 || number > 99 || seen.has(number)) return [];
      seen.add(number);
      const existing = subscriptions.find((row) => Number(row.season) === number);
      return [
        {
          season: number,
          title: season.name || `第 ${number} 季`,
          episode_count: Number(season.episode_count) || 0,
          poster_path: season.poster_path || media.poster_path || '',
          air_date: season.air_date || '',
          selected: false,
          start_episode: 1,
          state: existing ? 'conflict' : 'ready',
          subscription_state: existing?.state || '',
          message: existing ? '本季已有订阅' : '',
          id: existing?.id,
        },
      ];
    },
  );
}

export async function submitExploreSeasons(
  rows: ExploreSeasonRow[],
  media: ExploreActionMedia,
  submit: (request: Record<string, any>) => Promise<any>,
  changed: (subscription: any) => void,
  describe: (error: unknown) => string,
) {
  await submitSeasonBatch(
    rows,
    exploreSubscriptionBody(media),
    async (body) => {
      const subscription = await submit(body);
      if (!subscription?.id) throw new Error('订阅保存结果未确认，请刷新后重试');
      changed(subscription);
      return subscription;
    },
    describe,
  );
}

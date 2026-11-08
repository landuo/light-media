// Fixed MediaDetailView.getMediaSeasons consumes MediaInfo.season_info and puts
// specials last. Keep raw plugin dictionaries; the native compact list is only
// the fallback for older host records without an original MediaInfo response.
export function mediaDetailSeasons(media: any): any[] {
  const original = media?.original_media;
  const rows =
    original && Object.hasOwn(original, 'season_info') ? original.season_info : media?.seasons;
  if (!rows) return [];
  return [...rows].sort((a, b) => {
    if (a.season_number === 0) return 1;
    if (b.season_number === 0) return -1;
    return (a.season_number || 0) - (b.season_number || 0);
  });
}

export function subscriptionMediaIdentity(media?: {
  media_type?: string;
  id?: number | string;
  media_source?: string;
  media_id?: string;
}) {
  if (!media) return null;
  const kind = media.media_type;
  if (kind !== 'movie' && kind !== 'tv') return null;
  const source = media.media_source;
  const providerID = media.media_id;
  if (source != null || providerID != null) {
    if (
      Number(media.id || 0) !== 0 ||
      (source !== 'douban' && source !== 'bangumi' && source !== 'anilist') ||
      typeof providerID !== 'string' ||
      !/^[1-9]\d*$/.test(providerID)
    )
      return null;
    return { kind, source, id: providerID };
  }
  const id = Number(media.id);
  return Number.isSafeInteger(id) && id > 0 ? { kind, source: 'themoviedb', id } : null;
}

export function subscriptionMediaKey(media: Parameters<typeof subscriptionMediaIdentity>[0]) {
  const identity = subscriptionMediaIdentity(media);
  if (!identity) return '';
  const { kind, source, id } = identity;
  return source === 'themoviedb' ? `${kind}:${id}` : `${kind}:${source}:${id}`;
}

export function subscriptionTarget(input: Record<string, any>, convertLegacy = false) {
  if (input.media?.media_type === 'movie')
    return { target_scope: 'movie', start_episode: 0, episodes: [] as number[] };
  const start = Number(input.start_episode ?? 1);
  if (!Number.isInteger(start) || start < 1 || start > 9999)
    throw new Error('开始集数必须是 1 到 9999 的整数');
  const legacy =
    !!input.id && input.target_scope !== 'season' && input.episodes?.length && !convertLegacy;
  return {
    target_scope: legacy ? 'episodes' : 'season',
    start_episode: start,
    episodes: legacy ? [...input.episodes] : ([] as number[]),
  };
}
export function validateSubscriptionCounts(input: Record<string, any>) {
  if (input.media?.media_type !== 'tv') return;
  const start = Number(input.start_episode ?? 1),
    total = Number(input.total_episodes || 0);
  if (!Number.isInteger(start) || start < 1 || start > 9999)
    throw new Error('开始集数必须是 1 到 9999 的整数');
  if (!Number.isInteger(total) || total < 0 || total > 9999)
    throw new Error('总集数必须是 0 到 9999 的整数');
  if (total > 0 && start > total) throw new Error('开始集数不能大于总集数');
}
export function subscriptionTotal(input: Record<string, any>) {
  if (input.media?.media_type === 'movie') return 1;
  const start = input.start_episode || 1,
    cap = input.total_episodes || 0;
  const excluded = new Set(input.excluded_episodes || input.excluded || []);
  const known = input.known_episodes || input.target_episodes || input.episodes;
  if (known?.length)
    return known.filter((ep: number) => ep >= start && (!cap || ep <= cap) && !excluded.has(ep))
      .length;
  const total =
    cap ||
    mediaDetailSeasons(input.media).find((value: any) => value.season_number === input.season)
      ?.episode_count ||
    0;
  const excludedCount = [...excluded].filter((ep: any) => ep >= start && ep <= total).length;
  return Math.max(0, total - Math.max(0, start - 1) - excludedCount);
}

export type SeasonSubscriptionRow = {
  season: number;
  title: string;
  episode_count?: number;
  total_episodes?: number;
  poster_path?: string;
  selected: boolean;
  start_episode: number;
  state: 'ready' | 'running' | 'success' | 'conflict' | 'failed';
  message: string;
  id?: string;
};
export async function submitSeasonBatch(
  rows: SeasonSubscriptionRow[],
  template: Record<string, any>,
  submit: (request: Record<string, any>) => Promise<any>,
  describe: (error: unknown) => string = (error) =>
    error instanceof Error ? error.message : '请求失败',
) {
  for (const row of rows) {
    if (!row.selected || row.state === 'success' || row.state === 'conflict') continue;
    row.state = 'running';
    row.message = '';
    try {
      const target = subscriptionTarget({ ...template, id: '', start_episode: row.start_episode });
      const request = {
        ...template,
        ...target,
        season: row.season,
        total_episodes: Number(row.total_episodes ?? row.episode_count ?? 0),
      };
      validateSubscriptionCounts(request);
      const result = await submit(request);
      row.state = 'success';
      row.id = result?.id || '';
      row.message = '';
    } catch (error) {
      const code = String((error as { code?: string })?.code || '');
      row.state = /CONFLICT|EXISTS|DUPLICATE/.test(code) ? 'conflict' : 'failed';
      row.message = describe(error);
    }
  }
}

// Fixed SubscribeCard uses total - lack; a plugin existence observation is
// reflected in that persisted progress, without inventing a physical asset.
export function subscriptionDownloaded(input: Record<string, any>) {
  if (input.media?.media_type !== 'tv') return input.in_library_episodes?.length || 0;
  const total = input.total_episodes || 0;
  if (!total) return 0;
  return Math.min(Math.max(total - (input.lack_episode || 0), 0), total);
}
export function subscriptionProgressTotal(input: Record<string, any>) {
  return input.media?.media_type === 'tv' ? input.total_episodes || 0 : subscriptionTotal(input);
}

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
export function subscriptionTotal(input: Record<string, any>) {
  if (input.media?.media_type === 'movie') return 1;
  const excluded = new Set(input.excluded_episodes || input.excluded || []);
  const known = input.known_episodes || input.target_episodes || input.episodes;
  if (known?.length) return known.filter((ep: number) => !excluded.has(ep)).length;
  const total =
    input.media?.seasons?.find((v: any) => v.season_number === input.season)?.episode_count || 0;
  return Math.max(0, total - Math.max(0, (input.start_episode || 1) - 1) - excluded.size);
}

export type SeasonSubscriptionRow = {
  season: number;
  title: string;
  episode_count?: number;
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
      const result = await submit({ ...template, ...target, season: row.season });
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

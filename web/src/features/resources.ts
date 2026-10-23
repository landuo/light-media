export type Candidate = {
  resource: Record<string, any>;
  accepted?: boolean;
  needs_confirmation?: boolean;
  season?: number;
  episodes?: number[];
  reasons?: string[];
};
// A raw keyword search does not need a media identity before downloading. Keep
// unrelated parsing and user-rule explanations visible in that context.
export function resourceReviewReasons(candidate: Candidate | undefined, showIdentityReview = true) {
  return (candidate?.reasons || []).filter(
    (reason) => showIdentityReview || reason !== 'media_identity_required',
  );
}
export type ResourceFilters = {
  text: string;
  site: string | string[];
  resolution: string | string[];
  codec: string | string[];
  source: string | string[];
  promotion: string | string[];
  seeders: string;
  state: string;
  season: string | string[];
  group: string | string[];
};
export function filterValues(value: string | string[]) {
  return Array.isArray(value) ? value : value ? [value] : [];
}
function matchesFilter(value: string | string[], actual: string) {
  const selected = filterValues(value);
  return !selected.length || selected.includes(actual);
}
export const emptyResourceFilters = (): ResourceFilters => ({
  text: '',
  site: '',
  resolution: '',
  codec: '',
  source: '',
  promotion: '',
  seeders: '',
  state: '',
  season: '',
  group: '',
});
export function resourceGroup(c: Candidate) {
  return String(
    c.resource.release_group ||
      c.resource.title?.match(/-([A-Za-z0-9][A-Za-z0-9._]*)\s*$/)?.[1] ||
      '',
  );
}
export function candidateKey(c: Candidate) {
  return c.resource.site_id + ':' + c.resource.id;
}
export function promotionKind(r: Record<string, any>) {
  if (r.free === true || r.download_factor === 0) return 'free';
  if (typeof r.download_factor !== 'number') return 'unknown';
  if (r.download_factor < 1) return 'discount';
  if (typeof r.upload_factor === 'number' && r.upload_factor > 1) return 'upload';
  return 'normal';
}
export function filterResources(
  items: Candidate[],
  f: ResourceFilters,
  sort: string,
  descending: boolean,
  downloads: Record<string, string> = {},
) {
  const result = items.filter((c) => {
    const r = c.resource;
    if (!matchesFilter(f.season, String(c.season || 0))) return false;
    if (!matchesFilter(f.group, resourceGroup(c))) return false;
    if (
      f.text &&
      !(String(r.title || '') + ' ' + String(r.subtitle || ''))
        .toLowerCase()
        .includes(f.text.toLowerCase())
    )
      return false;
    for (const k of ['site', 'resolution', 'codec', 'source'] as const) {
      const value = String(r[k === 'site' ? 'site_id' : k] || 'unknown');
      if (!matchesFilter(f[k], value)) return false;
    }
    if (!matchesFilter(f.promotion, promotionKind(r))) return false;
    const seeders = typeof r.seeders === 'number' ? r.seeders : -1;
    if (
      (f.seeders === 'unknown' && seeders >= 0) ||
      (f.seeders === 'alive' && seeders <= 0) ||
      (f.seeders === 'zero' && seeders !== 0)
    )
      return false;
    if (
      (f.state === 'downloaded' && !downloads[candidateKey(c)]) ||
      (f.state === 'new' && downloads[candidateKey(c)]) ||
      (f.state === 'matched' && !c.accepted) ||
      (f.state === 'review' && !c.needs_confirmation)
    )
      return false;
    return true;
  });
  if (sort !== 'default')
    result.sort((a, b) => {
      if (sort === 'site')
        return (
          String(a.resource.site_id).localeCompare(String(b.resource.site_id)) *
          (descending ? -1 : 1)
        );
      const value = (c: Candidate): number | null => {
        const v = sort === 'published_at' ? Date.parse(c.resource.published_at) : c.resource[sort];
        if (sort === 'size' && !(v > 0)) return null;
        return typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : null;
      };
      const x = value(a),
        y = value(b);
      // Unknown values remain last in either direction, never masquerading as zero.
      if (x === null) return y === null ? 0 : 1;
      if (y === null) return -1;
      return (x - y) * (descending ? -1 : 1);
    });
  return result;
}
export function groupResources(items: Candidate[]) {
  const groups = new Map<string, Candidate[]>();
  for (const c of items) {
    const r = c.resource;
    // Only the same release title, size and episode range can share a card.
    // Unknown sizes stay independent: no guessed cross-site identity.
    const key =
      r.size > 0
        ? [
            String(r.title).trim().toLowerCase(),
            r.size,
            c.season || 0,
            (c.episodes || []).join(','),
          ].join('|')
        : candidateKey(c);
    const group = groups.get(key);
    if (group) group.push(c);
    else groups.set(key, [c]);
  }
  return [...groups.values()].map((group) =>
    group.sort(
      (a, b) =>
        Number(!!b.accepted) - Number(!!a.accepted) ||
        Number(!!b.needs_confirmation) - Number(!!a.needs_confirmation),
    ),
  );
}
export function promotionRemaining(end: string, now = Date.now()) {
  const delta = Date.parse(end) - now;
  if (!Number.isFinite(delta)) return '';
  if (delta <= 0) return '已到期';
  const hours = Math.ceil(delta / 3600000);
  return hours >= 24
    ? '剩余 ' + Math.floor(hours / 24) + '天 ' + (hours % 24) + '小时'
    : '剩余 ' + hours + '小时';
}

// Candidate episode syntax can suggest a target only inside the selected season.
// An explicitly selected search range always takes precedence over that hint.
export function suggestedDownloadEpisodes(
  explicit: number[],
  candidate: Candidate,
  season: number,
) {
  if (explicit.length) return [...explicit];
  if (candidate.season !== season || !candidate.episodes?.length) return [];
  const unique = [...new Set(candidate.episodes)];
  return unique.every((ep) => Number.isInteger(ep) && ep > 0 && ep <= 9999) ? unique : [];
}

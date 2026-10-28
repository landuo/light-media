import { image } from '../api.ts';
import { apiURL } from '../base-path.ts';

export function exploreImage(media: any, path = media.poster_path, size = 'w500') {
  if (!path) return '';
  const source = media.source || 'themoviedb';
  if (source === 'themoviedb') return image(path, size);
  if (!['douban', 'bangumi', 'anilist'].includes(source)) {
    try {
      const url = new URL(path);
      return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password
        ? url.href
        : '';
    } catch {
      return '';
    }
  }
  return apiURL('/catalog/explore/image?' + new URLSearchParams({ source, url: path }));
}

export function exploreDetailRoute(media: any) {
  const path =
    'explore-detail/' +
    [media.source || 'themoviedb', media.media_type, media.source_id || media.id]
      .map(encodeURIComponent)
      .join('/');
  const hints = new URLSearchParams();
  if (typeof media.title === 'string' && media.title) hints.set('title', media.title);
  if (media.year != null && String(media.year)) hints.set('year', String(media.year));
  return path + (hints.size ? '?' + hints : '');
}

export function parseExploreDetailRoute(route: string) {
  const [, rawSource = '', rawKind = '', rawID = ''] = route.split('?')[0]!.split('/');
  const hints = new URLSearchParams(route.includes('?') ? route.slice(route.indexOf('?') + 1) : '');
  return {
    source: decodeURIComponent(rawSource),
    kind: decodeURIComponent(rawKind),
    sourceId: decodeURIComponent(rawID),
    ...(hints.has('title') ? { title: hints.get('title')! } : {}),
    ...(hints.has('year') ? { year: hints.get('year')! } : {}),
  };
}

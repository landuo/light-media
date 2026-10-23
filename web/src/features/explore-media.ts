import { image } from '../api';
import { apiURL } from '../base-path';

export function exploreImage(media: any, path = media.poster_path, size = 'w500') {
  if (!path) return '';
  const source = media.source || 'themoviedb';
  if (source === 'themoviedb') return image(path, size);
  if (!['douban', 'bangumi', 'anilist'].includes(source)) return '';
  return apiURL('/catalog/explore/image?' + new URLSearchParams({ source, url: path }));
}

export function exploreDetailRoute(media: any) {
  return (
    'explore-detail/' +
    [media.source || 'themoviedb', media.media_type, media.source_id || media.id]
      .map(encodeURIComponent)
      .join('/')
  );
}

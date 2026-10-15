import { searchStream } from '../api.ts';
export async function keywordResourceSearch(
  body: Record<string, any>,
  signal: AbortSignal,
  progress: (value: any) => void = () => {},
) {
  return searchStream(body, progress, signal, '/search/resources');
}
export function resourceTitleQuery(title: string, fallback = '') {
  const clean = title
    .replace(/[._]+/g, ' ')
    .replace(/\[[^\]]*\]/g, ' ')
    .trim();
  const before = clean
    .split(
      /\b(?:19\d{2}|20\d{2}|S\d{1,3}(?:E\d+)?|\d{3,4}p|WEB[- ]?DL|WEBRip|BluRay|HEVC|AVC)\b/i,
    )[0]
    ?.trim();
  return before || fallback || clean;
}

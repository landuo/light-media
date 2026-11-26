import { APIError, searchStream } from '../api.ts';
import { boundedRead } from './bounded-read.ts';
export async function keywordResourceSearch(
  body: Record<string, any>,
  signal: AbortSignal,
  progress: (value: any) => void = () => {},
) {
  let shown = 0;
  return boundedRead(async (transport) => {
    try {
      return await searchStream(body, (value) => {
        shown = value.candidates?.length || 0;
        progress(value);
      }, transport, '/search/resources');
    } catch (error) {
      if (transport.aborted && !signal.aborted) {
        const detail = shown ? `已保留 ${shown} 个资源` : '尚未收到可展示的结果';
        throw new APIError('search_wait_timeout', `等待已满 15 秒，${detail}，未收到最终搜索结果。`, '');
      }
      throw error;
    }
  }, signal, 15000);
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

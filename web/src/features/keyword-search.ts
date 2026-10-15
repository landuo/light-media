import { APIError, apiURL, ui } from '../api.ts';
export async function keywordResourceSearch(body: Record<string, any>, signal: AbortSignal) {
  ui.busy++;
  try {
    const response = await fetch(apiURL('/search/resources'), {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal,
    });
    let value: any;
    try {
      value = await response.json();
    } catch {
      throw new APIError(
        'INVALID_RESPONSE',
        '服务返回了无法读取的响应',
        response.headers.get('X-Request-ID') || '',
      );
    }
    if (!response.ok || value.error) {
      if (response.status === 401 && typeof window !== 'undefined')
        window.dispatchEvent(new Event('session-expired'));
      throw new APIError(
        value.error?.code || 'SEARCH_FAILED',
        value.error?.message || '资源搜索失败',
        value.request_id || response.headers.get('X-Request-ID') || '',
      );
    }
    return value.data;
  } finally {
    ui.busy--;
  }
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

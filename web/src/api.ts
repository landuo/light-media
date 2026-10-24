import { reactive } from 'vue';
import { apiURL } from './base-path.ts';
export { apiURL } from './base-path.ts';
export type Any = Record<string, any>;
const messages: Record<string, string> = {
  media_identity_required: '下载前请选择影视身份并确认季集',
  site_api_key_required: '请填写站点 API Key',
  site_cookie_required: '请填写站点 Cookie',
  site_authentication_failed: '站点认证失败，请更新凭据',
  site_forbidden_or_challenge: '站点拒绝请求或要求浏览器验证',
  site_login_challenge: '站点要求浏览器验证，请先在站点处理',
  site_authentication_or_template_changed: '登录状态或页面结构已变化，请检查站点',
  site_rate_limited: '站点请求过于频繁，请稍后重试',
  site_transport_error: '无法连接站点',
  site_protocol_changed: '站点返回结构已变化，已停止解析以免误判',
  site_business_error: '站点业务请求失败，请检查 API Key 或站点状态',
  site_user_identity_missing: '站点未返回有效账户信息，请检查凭据',
  site_rousi_origin_not_allowed: '肉丝站点请使用 https://rousi.pro 根地址',
  site_material_origin_blocked: '种子地址不属于当前站点，已停止下载',
  site_http_error: '站点返回 HTTP 错误',
  site_invalid_metainfo: '下载材料不是有效种子',
  site_body_limit: '站点响应超过大小限制',
  site_https_required: '站点凭据必须使用 HTTPS',
  site_public_https_origin_required: '通用站点需要可直接访问的公网 HTTPS 域名',
  site_invalid_origin: '站点网址无效，请填写完整的 HTTPS 根地址',
  site_invalid_config: '站点连接配置无效，请核对凭据、User-Agent 和超时',
  site_material_redirect_blocked: '下载材料重定向目标未获允许',
  site_download_link_missing: '详情页没有找到匹配的下载链接',
  site_invalid_size: '站点资源体积无法识别',
  site_invalid_seeders: '站点做种人数无法识别',
  site_invalid_leechers: '站点下载人数无法识别',
  missing_or_ambiguous_year_or_episode: '文件名中的年份或季集不明确，需要人工确认',
  insufficient_seeders: '做种人数低于规则要求',
  seeders_unknown: '做种人数未知',
  resource_size_unknown: '资源体积未知',
  below_minimum_size: '低于最小体积',
  above_maximum_size: '超过最大体积',
  free_promotion_not_confirmed: '未确认免费促销',
  free_promotion_expired: '免费促销已结束',
  promotion_expiry_unknown: '促销到期时间未知',
  include_not_matched: '未匹配包含规则',
  excluded_by_pattern: '匹配排除规则',
  title_mismatch: '影视名称不匹配',
  series_numbering_on_movie: '电影资源中出现剧集编号',
  movie_year_conflict: '电影年份存在冲突，需要核对',
  movie_year_unconfirmed: '电影年份未确认',
  series_year_conflict: '剧集年份存在冲突，需要核对',
  season_pack_requires_confirmation: '整季资源包，需要人工确认',
  episode_numbering_unresolved: '资源集号尚未解析',
  episode_not_confirmed_aired: '目标集尚未确认播出',
  episode_metadata_unavailable: '单集元数据尚不可用',
  resource_episode_outside_target: '资源包含目标之外的集数',
  resource_season_outside_target: '资源季号与目标不一致',
  episode_not_in_authoritative_metadata: '集号未被标准元数据确认',
  numbering_mapping_required_for_absolute_numbering: '绝对集号需要明确映射',
  resolution_mismatch: '分辨率不符合要求',
  original_filter_not_matched: '不符合订阅附加筛选规则',
  invalid_original_filter: '订阅附加筛选规则无效或匹配超时',
  codec_mismatch: '编码不符合要求',
  resolution_unknown: '分辨率未知',
  codec_unknown: '编码未知',
  resolution_conflict: '资源分辨率存在冲突',
  codec_conflict: '资源编码存在冲突',
};
export function friendly(value: string) {
  return messages[value] || value;
}
export const ui = reactive({
  busy: 0,
  toasts: [] as { id: number; text: string; error: boolean }[],
  settings: null as Any | null,
  status: null as Any | null,
});
let serial = 0;
export function toast(text: string, error = false) {
  const id = ++serial;
  ui.toasts.push({ id, text, error });
  setTimeout(
    () => {
      ui.toasts = ui.toasts.filter((t) => t.id !== id);
    },
    error ? 12000 : 4500,
  );
}
export class APIError extends Error {
  constructor(
    public code: string,
    message: string,
    public requestId: string,
  ) {
    super(friendly(message));
  }
}
export function errorText(e: unknown) {
  const err = e as APIError;
  return `${err.message || '操作失败'}${err.requestId ? ' · 请求 ' + err.requestId : ''}`;
}
export async function api<T = any>(path: string, body?: any, method?: string): Promise<T> {
  ui.busy++;
  try {
    const r = await fetch(apiURL(path), {
      method: method || (body === undefined ? 'GET' : 'POST'),
      credentials: 'same-origin',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    let j: any;
    try {
      j = await r.json();
    } catch {
      throw new APIError('invalid_response', '服务返回了无法读取的响应', '');
    }
    if (!r.ok || j.error) {
      if (r.status === 401) window.dispatchEvent(new Event('session-expired'));
      throw new APIError(
        j.error?.code || 'request_failed',
        j.error?.message || '请求失败',
        j.request_id || '',
      );
    }
    return j.data as T;
  } finally {
    ui.busy--;
  }
}
export async function action<T = any>(
  fn: () => Promise<T>,
  success?: string,
): Promise<T | undefined> {
  try {
    const r = await fn();
    if (success) toast(success);
    return r;
  } catch (e) {
    toast(errorText(e), true);
  }
}
// The response carries cumulative per-site snapshots. A dropped/aborted stream
// never becomes a fabricated successful search.
export async function searchStream(
  body: Any,
  progress: (value: any) => void,
  signal: AbortSignal,
  path: '/search' | '/search/resources' = '/search',
): Promise<any> {
  ui.busy++;
  try {
    const response = await fetch(apiURL(path), {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
      body: JSON.stringify(body),
      signal,
    });
    const requestId = response.headers.get('X-Request-ID') || '';
    if (!response.ok || !response.headers.get('Content-Type')?.includes('text/event-stream')) {
      const j = await response.json();
      if (!response.ok || j.error) {
        if (response.status === 401 && typeof window !== 'undefined')
          window.dispatchEvent(new Event('session-expired'));
        throw new APIError(
          j.error?.code || 'search_failed',
          j.error?.message || '资源搜索失败',
          j.request_id || requestId,
        );
      }
      progress(j.data);
      return j.data;
    }
    if (!response.body)
      throw new APIError('stream_unavailable', '服务未返回资源搜索数据流', requestId);
    const reader = response.body.getReader(),
      decoder = new TextDecoder();
    let buffer = '',
      complete: any;
    function consume(frame: string) {
      let event = 'message';
      const data: string[] = [];
      for (const line of frame.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim();
        else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
      }
      if (!data.length) return;
      let value: any;
      try {
        value = JSON.parse(data.join('\n'));
      } catch {
        throw new APIError('invalid_response', '资源搜索返回了无效数据', requestId);
      }
      if (event === 'error') {
        const err = value.error || value;
        throw new APIError(
          err.code || 'search_failed',
          err.message || '资源搜索失败',
          value.request_id || requestId,
        );
      }
      if (event === 'progress') {
        progress(value.data || value);
      }
      if (event === 'complete') {
        complete = value.data || value;
        progress(complete);
      }
    }
    try {
      while (true) {
        const chunk = await reader.read();
        buffer += decoder.decode(chunk.value || new Uint8Array(), { stream: !chunk.done });
        buffer = buffer.replace(/\r\n/g, '\n');
        let end: number;
        while ((end = buffer.indexOf('\n\n')) >= 0) {
          const frame = buffer.slice(0, end);
          buffer = buffer.slice(end + 2);
          consume(frame);
        }
        if (complete !== undefined) return complete;
        if (buffer.length > 8 * 1024 * 1024)
          throw new APIError('stream_limit', '资源搜索响应超过限制', requestId);
        if (chunk.done) {
          if (buffer.trim()) consume(buffer);
          break;
        }
      }
      if (complete !== undefined) return complete;
      throw new APIError(
        'stream_interrupted',
        '资源搜索连接中断，已返回部分结果可供查看',
        requestId,
      );
    } finally {
      await reader.cancel().catch(() => {});
    }
  } finally {
    ui.busy--;
  }
}
export async function refreshSettings() {
  ui.settings = await api('/settings');
  return ui.settings!;
}
export async function refreshStatus() {
  ui.status = await api('/status');
  return ui.status!;
}
export function go(path: string) {
  location.hash = path;
}
export function image(path: string, size = 'w342') {
  if (!path || !/^\/[A-Za-z0-9._/-]+$/.test(path) || path.startsWith('//') || path.includes('..'))
    return '';
  const safeSize = ['w92', 'w185', 'w342', 'w500', 'w780', 'w1280'].includes(size) ? size : 'w342';
  return apiURL('/catalog/image?' + new URLSearchParams({ path, size: safeSize }));
}
export function bytes(n: number) {
  if (!n) return '0 B';
  const p = Math.min(4, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / 1024 ** p).toFixed(p ? 1 : 0)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][p]}`;
}
export function materialLabel(item: any) {
  if (item.material_state === 'archived' && item.material_ref) return '加密归档材料可用';
  if (item.material_state === 'metainfo_unavailable' || !item.material_ref) return '无可用归档材料';
  return '归档材料状态待核验';
}
export function date(value: any) {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.valueOf()) ? String(value) : d.toLocaleString('zh-CN', { hour12: false });
}
export const labels: Record<string, string> = {
  queued: '等待执行',
  running: '进行中',
  completed: '已完成',
  succeeded: '已完成',
  failed: '失败',
  paused: '已暂停',
  pending: '等待中',
  ready: '就绪',
  active: '订阅中',
  watching: '监控中',
  planned: '准备提交',
  accepted: '下载器已接收',
  downloading: '下载中',
  downloaded: '下载完成',
  importing: '整理中',
  imported: '已入库',
  blocked: '待处理',
  cancelled: '已取消',
  canceled: '已取消',
  missing: '文件缺失',
  missing_confirmed: '已确认缺失',
  missing_pending: '缺失待确认',
  unknown: '待确认',
  disabled: '未启用',
  unverified: '未核验',
  removed: '已移除',
  movie: '电影',
  tv: '剧集',
  hardlink: '硬链接',
  copy: '复制',
  auto: '自动',
  success: '成功',
  retry_wait: '等待重试',
  waiting_paused: '暂停等待',
  pending_confirmation: '集号待确认',
  predicted: '周期预测',
  skipped: '已跳过',
  postponed: '已延期',
  excluded: '已排除',
  present: '文件存在',
  managed: '自管资产',
  reconcile_pending: '等待核对提交结果',
  scraped: '已刮削',
  synced: '已同步',
};
export function label(v: string) {
  return labels[v] || v || '—';
}
Object.assign(labels, {
  missing_suspected: '疑似缺失',
  missing_pending: '缺失待确认',
  missing_confirmed: '已确认缺失',
  changed: '文件已变化',
  external: '外部资产',
  present: '文件存在',
  scraped: '已刮削',
  visible: '飞牛可见',
  download: '下载提交',
  import: '文件整理',
  scrape: '元数据刮削',
  fnos_sync: '飞牛同步',
  library_scan: '媒体库核对',
  asset_check: '资产检查',
  subscription: '订阅检查',
});
export function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v));
}
export function episodeNumbers(value: string) {
  return [
    ...new Set(
      value
        .split(/[,，\s]+/)
        .filter(Boolean)
        .flatMap((p) => {
          const m = p.match(/^(\d+)-(\d+)$/);
          if (m) {
            const a = +m[1]!,
              b = +m[2]!;
            if (b < a || b - a > 1000) throw new Error('集数范围无效');
            return Array.from({ length: b - a + 1 }, (_, i) => a + i);
          }
          if (!/^\d+$/.test(p)) throw new Error('集数格式应为 1,2,5-8');
          return [+p];
        }),
    ),
  ];
}
export const emptyPolicy = () => ({
  min_seeders: 1,
  free_only: false,
  resolution: '',
  codec: '',
  source: '',
  hdr: '',
  languages: [] as string[],
  subtitles: [] as string[],
  preferred_groups: [] as string[],
  preferred_resolutions: [] as string[],
  min_bytes: 0,
  max_bytes: 0,
  include: '',
  exclude: '',
});
export const emptySchedule = () => ({
  source: 'metadata',
  rule: 'weekly',
  weekdays: [5],
  every_days: 1,
  start_date: new Date().toLocaleDateString('en-CA'),
  time: '20:00',
  timezone: 'Asia/Shanghai',
  mode: 'follow',
  interval_minutes: 360,
  wait_minutes: 30,
  window_hours: 6,
  active_minutes: 30,
  fallback_minutes: 360,
  exceptions: [] as Any[],
});

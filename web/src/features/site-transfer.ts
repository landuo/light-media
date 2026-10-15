import { APIError, apiURL } from '../api.ts';

export type SiteImportResult = {
  total: number;
  imported: number;
  created: number;
  updated: number;
  skipped: number;
  failed: number;
  results: {
    index: number;
    name: string;
    status: 'created' | 'updated' | 'skipped' | 'failed';
    message: string;
    warnings: string[];
  }[];
};

export async function readSiteImport(file: Pick<File, 'size' | 'text'>): Promise<unknown> {
  if (file.size > 2 * 1024 * 1024) throw new Error('站点配置文件不能超过 2 MiB');
  let text: string;
  try {
    text = await file.text();
  } catch {
    throw new Error('无法读取选择的文件，请重新选择');
  }
  try {
    return JSON.parse(text.replace(/^\uFEFF/, ''));
  } catch {
    // Browser JSON errors can quote credential-bearing parts of the file.
    throw new Error('文件不是有效的 JSON，请选择 MoviePilot 或本应用导出的站点配置');
  }
}

export function confirmedSiteImport(value: unknown): SiteImportResult {
  const result = value as SiteImportResult;
  if (
    !result ||
    !['total', 'imported', 'created', 'updated', 'skipped', 'failed'].every(
      (key) => Number.isSafeInteger((result as any)[key]) && (result as any)[key] >= 0,
    ) ||
    result.imported !== result.created + result.updated ||
    result.total !== result.imported + result.skipped + result.failed ||
    !Array.isArray(result.results) ||
    result.results.length !== result.total ||
    !result.results.every(
      (row) =>
        row &&
        ['created', 'updated', 'skipped', 'failed'].includes(row.status) &&
        typeof row.name === 'string' &&
        typeof row.message === 'string' &&
        Array.isArray(row.warnings) &&
        row.warnings.every((warning) => typeof warning === 'string'),
    )
  )
    throw new Error('服务未返回完整导入结果，请重新加载站点核对');
  return result;
}

export async function downloadSiteExport() {
  const response = await fetch(apiURL('/sites/export'), {
    credentials: 'same-origin',
    cache: 'no-store',
  });
  let payload: any;
  try {
    payload = await response.json();
  } catch {
    throw new Error('服务未返回有效的站点导出文件');
  }
  if (!response.ok || payload?.error) {
    if (response.status === 401) window.dispatchEvent(new Event('session-expired'));
    throw new APIError(
      payload?.error?.code || 'site_export_failed',
      payload?.error?.message || '站点导出失败',
      payload?.request_id || response.headers.get('X-Request-ID') || '',
    );
  }
  if (
    payload?.format !== 'light-media-sites' ||
    payload.version !== 1 ||
    !Array.isArray(payload.sites)
  )
    throw new Error('服务未返回有效的站点导出文件');
  const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  const filename = response.headers
    .get('Content-Disposition')
    ?.match(/filename="?([a-zA-Z0-9._-]+\.json)"?(?:;|$)/)?.[1];
  link.href = url;
  link.download =
    filename || `light-media-sites-${new Date().toISOString().slice(0, 10).replace(/-/g, '')}.json`;
  try {
    document.body.appendChild(link);
    link.click();
  } finally {
    link.remove();
    URL.revokeObjectURL(url);
  }
}

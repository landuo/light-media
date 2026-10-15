import { APIError, apiURL, errorText } from '../api.ts';

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

export type SiteImportDocument = { format: 'moviepilot' | 'light-media'; sites: any[] };
export function siteImportDocument(value: any): SiteImportDocument {
  const native = value?.format === 'light-media-sites' && value.version === 1;
  const sites = Array.isArray(value) ? value : native ? value.sites : null;
  if (!Array.isArray(sites)) throw new Error('请选择 MoviePilot 或本应用导出的站点 JSON 文件');
  if (!sites.length) throw new Error('文件中没有可导入的站点');
  if (sites.length > 128) throw new Error('一次最多导入 128 个站点');
  return { format: native ? 'light-media' : 'moviepilot', sites };
}
export function sitePreview(item: any, index: number) {
  let url = '';
  try {
    const parsed = new URL(item?.url || item?.base_url || '');
    if (['http:', 'https:'].includes(parsed.protocol) && parsed.hostname)
      url = parsed.origin + (parsed.pathname === '/' ? '' : parsed.pathname);
  } catch {
    /* Invalid entries stay visible and receive an explicit import result. */
  }
  return {
    name:
      typeof item?.name === 'string' && item.name.trim()
        ? item.name.slice(0, 64)
        : `第 ${index + 1} 项`,
    url,
    valid: !!url,
  };
}
export async function importSiteDocument(
  document: SiteImportDocument,
  submit: (body: any) => Promise<unknown>,
  progress: (completed: number, total: number) => void,
): Promise<SiteImportResult> {
  const report: SiteImportResult = {
    total: document.sites.length,
    imported: 0,
    created: 0,
    updated: 0,
    skipped: 0,
    failed: 0,
    results: [],
  };
  progress(0, report.total);
  for (const [index, item] of document.sites.entries()) {
    const body =
      document.format === 'light-media'
        ? { format: 'light-media-sites', version: 1, sites: [item] }
        : [item];
    try {
      const result = confirmedSiteImport(await submit(body));
      if (result.total !== 1) throw new Error('服务未确认该站点的导入结果，请重新加载站点核对');
      for (const key of ['imported', 'created', 'updated', 'skipped', 'failed'] as const)
        report[key] += result[key];
      const outcome = result.results[0]!;
      // Each request contains one item, so a validation failure has a local
      // fallback name. Restore the file position without echoing raw values.
      let name = outcome.name;
      if (
        ['failed', 'skipped'].includes(outcome.status) &&
        (!name.trim() || /^第\s*1\s*项$/.test(name))
      ) {
        const preview = sitePreview(item, index);
        name = preview.valid ? new URL(preview.url).hostname : `第 ${index + 1} 项`;
      }
      report.results.push({ ...outcome, name, index: index + 1 });
    } catch (e) {
      report.failed++;
      report.results.push({
        index: index + 1,
        name: `第 ${index + 1} 项`,
        status: 'failed',
        message: errorText(e),
        warnings: [],
      });
    }
    progress(index + 1, report.total);
  }
  return report;
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
    !Array.isArray(payload) &&
    (payload?.format !== 'light-media-sites' ||
      payload.version !== 1 ||
      !Array.isArray(payload.sites))
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

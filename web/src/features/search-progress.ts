const pending = new Set(['pending', 'queued', 'loading', 'searching']);
export function siteSearchLabel(status: string) {
  return (
    (
      {
        ok: '完成',
        partial: '部分结果',
        empty: '无结果',
        failed: '失败',
        timeout: '超时',
        cancelled: '已取消',
        interrupted: '连接中断',
        loading: '搜索中',
        searching: '搜索中',
        pending: '待搜索',
        queued: '排队中',
      } as Record<string, string>
    )[status] || status
  );
}
export function searchPending(status: string) {
  return pending.has(status);
}
export function interruptedSearch(output: any, siteIDs: string[], status = 'cancelled') {
  const previous = output?.sites || [];
  return {
    ...output,
    candidates: output?.candidates || [],
    sites: siteIDs.map((id) => {
      const site = previous.find((s: any) => s.id === id);
      return !site || pending.has(site.status)
        ? { ...site, id, status, count: site?.count || 0 }
        : site;
    }),
  };
}
export function siteElapsed(site: { elapsed_ms?: number }) {
  return site.elapsed_ms === undefined ? '' : ' · ' + (site.elapsed_ms / 1000).toFixed(1) + ' 秒';
}

const pages = new Set([
  'discover',
  'detail',
  'search',
  'subscriptions',
  'downloads',
  'import',
  'assets',
  'jobs',
  'settings',
  'logs',
  'calendar',
  'person',
  'workflows',
]);
const pageAliases: Record<string, string> = {
  resources: 'search',
  organize: 'import',
  library: 'assets',
};
let windowStart = 0;
let reports = 0;

// Report fixed classifications only. Form values, URL parameters, exception
// messages and stacks must never leave the browser in a diagnostic report.
export function reportFrontendError(
  code: 'VUE_RENDER_ERROR' | 'UNHANDLED_REJECTION' | 'SCRIPT_ERROR',
) {
  const now = Date.now();
  if (now - windowStart >= 60000) {
    windowStart = now;
    reports = 0;
  }
  if (reports++ >= 10) return;
  const segment = location.hash.slice(1).split(/[/?]/, 1)[0] || 'home';
  const candidate = pageAliases[segment] || segment;
  const page = pages.has(candidate) ? candidate : 'home';
  const messages = {
    VUE_RENDER_ERROR: '页面渲染发生异常',
    UNHANDLED_REJECTION: '页面异步操作异常',
    SCRIPT_ERROR: '页面脚本执行异常',
  };
  void fetch('/api/frontend-errors', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ page, code, request_id: '', build: '0.1.0', message: messages[code] }),
  }).catch(() => {});
}

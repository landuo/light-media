import type { PluginCard } from './plugin-forms';

type ProjectMetadata = Pick<PluginCard, 'instance_id' | 'repo_url' | 'author_url'>;
type ProjectPopup = { opener: unknown; location: { replace(url: string): void }; close(): void };

export function pluginProjectURL(plugin: Partial<ProjectMetadata>) {
  const safe = (value: unknown) => {
    if (typeof value !== 'string') return '';
    try {
      const url = new URL(value);
      if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) return '';
      if (url.hostname === 'raw.githubusercontent.com') {
        const [owner, repo] = url.pathname.split('/').filter(Boolean);
        if (owner && repo) return `https://github.com/${owner}/${repo}`;
      }
      return url.href;
    } catch {
      return '';
    }
  };
  return safe(plugin.repo_url) || safe(plugin.author_url);
}

// Open while handling the original click, before asynchronous metadata reads,
// so browsers do not treat the final navigation as an unsolicited popup.
export async function openPluginProject(
  plugin: ProjectMetadata,
  request: (path: string) => Promise<any>,
  open: (url: string, target: string) => ProjectPopup | null = (url, target) =>
    window.open(url, target),
) {
  const popup = open('about:blank', '_blank');
  if (popup) popup.opener = null;
  let detail: Partial<ProjectMetadata> = { ...plugin };
  try {
    const history = await request(
      '/plugins/history/' + encodeURIComponent(plugin.instance_id) + '?force=false',
    );
    if (history && typeof history === 'object' && !Array.isArray(history))
      detail = { ...detail, ...history };
  } catch {
    /* Original history is best effort; source/author fallbacks remain usable. */
  }
  if (!detail.repo_url || detail.repo_url.startsWith('local://')) {
    try {
      const market = await request('/plugins/market?force=false');
      if (Array.isArray(market)) {
        const candidate = market.find((item) => item.instance_id === plugin.instance_id);
        if (candidate) detail = { ...detail, ...candidate };
      }
    } catch {
      /* A known author address is still useful without a repository response. */
    }
  }
  const url = pluginProjectURL(detail);
  if (!url) {
    popup?.close();
    throw new Error('插件未提供可用的项目或作者主页');
  }
  if (popup) popup.location.replace(url);
  else {
    const fallback = open(url, '_blank');
    if (fallback) fallback.opener = null;
  }
  return url;
}

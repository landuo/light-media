export type DownloadDirectory = {
  id: string;
  name: string;
  downloader_id: string;
  download_path: string;
  root_id: string;
  media_type: string;
  mode: string;
  enabled: boolean;
  monitor_type?: string;
  source_path?: string;
};
function normalized(path: string) {
  return path.replace(/\/+$/, '') || '/';
}
export function directoriesFor(
  directories: DownloadDirectory[],
  downloaderID: string,
  mediaType = '',
) {
  return directories.filter(
    (d) =>
      d.downloader_id === downloaderID &&
      !!d.download_path &&
      (!mediaType || !d.media_type || d.media_type === 'mixed' || d.media_type === mediaType),
  );
}
function contains(prefix: string, path: string) {
  return (
    !!prefix &&
    (path === normalized(prefix) ||
      path.startsWith(normalized(prefix) === '/' ? '/' : normalized(prefix) + '/'))
  );
}
function applicationPath(path: string, mappings: { from: string; to: string }[]) {
  if (!mappings.length) return path;
  const m = [...mappings]
    .filter((m) => contains(m.from, path))
    .sort((a, b) => b.from.length - a.from.length)[0];
  return m ? normalized(m.to) + path.slice(normalized(m.from).length) : '';
}
export function matchingDirectory(
  directories: DownloadDirectory[],
  downloaderID: string,
  downloadPath: string,
  mediaType = '',
  mappings: { from: string; to: string }[] = [],
) {
  if (!downloadPath) return undefined;
  const path = normalized(downloadPath),
    source = applicationPath(path, mappings);
  return directories
    .filter(
      (d) => !mediaType || !d.media_type || d.media_type === 'mixed' || d.media_type === mediaType,
    )
    .map((d) => ({
      rule: d,
      prefix:
        d.downloader_id === downloaderID && d.download_path
          ? applicationPath(normalized(d.download_path), mappings)
          : !d.downloader_id && d.monitor_type === 'monitor'
            ? d.source_path || ''
            : '',
    }))
    .filter((d) => contains(d.prefix, source))
    .sort(
      (a, b) =>
        normalized(b.prefix).length - normalized(a.prefix).length ||
        Number(!!b.rule.downloader_id) - Number(!!a.rule.downloader_id) ||
        Number(b.rule.media_type === mediaType) - Number(a.rule.media_type === mediaType),
    )[0]?.rule;
}
export function downloadDestination(input: Record<string, any>) {
  const downloader = String(input.downloader_id || '').trim();
  const path = String(input.download_path || '').trim();
  if (!downloader) throw new Error('请选择下载器');
  if (!path.startsWith('/') || path.split('/').some((part) => part === '..'))
    throw new Error('请填写下载器可访问的绝对下载目录');
  return {
    downloader_id: downloader,
    download_path: path,
    directory_id: String(input.directory_id || ''),
  };
}

export type DownloadDirectory = {
  id: string;
  name: string;
  source_path: string;
  library_path?: string;
  media_type?: string;
  mode?: string;
  enabled?: boolean;
  monitor_type?: string;
};
function normalized(path: string) {
  return path.replace(/\/+$/, '') || '/';
}
// MoviePilot's download dialog lists every configured resource directory. The
// choice is independent of the selected downloader and the organization target.
export function directoriesFor(directories: DownloadDirectory[]) {
  return directories.filter((directory) => !!directory.source_path);
}
export function downloadDirectoryPaths(directories: DownloadDirectory[]) {
  return [
    ...new Set(directoriesFor(directories).map((directory) => normalized(directory.source_path))),
  ];
}
export function downloadDestination(input: Record<string, any>) {
  const downloader = String(input.downloader_id || '').trim();
  const path = String(input.download_path || '').trim();
  if (path && (!path.startsWith('/') || path.split('/').some((part) => part === '..')))
    throw new Error('请填写绝对下载目录');
  return { downloader_id: downloader, download_path: path };
}

export function downloadTaskInfo(data: any, downloaderID: string, task: any) {
  const hash = String(task.hash || '').toLowerCase();
  const records = [...(data?.intents || []), ...(data?.native_downloads || [])];
  const record = records.find(
    (item: any) =>
      item.downloader_id === downloaderID &&
      String(item.hash || '').toLowerCase() === hash &&
      item.state !== 'cancelled',
  );
  const source = task.source_torrent;
  const sourceMedia = source?.media;
  const media =
    record?.media ||
    (sourceMedia && Object.keys(sourceMedia).length
      ? {
          ...sourceMedia,
          poster_path: sourceMedia.poster || sourceMedia.image,
          backdrop_path: sourceMedia.backdrop,
        }
      : undefined);
  const season = Number(record?.season || 0);
  const episodes: number[] = record?.episodes || [];
  const episodeText = episodes.length
    ? episodes.length > 1
      ? `E${String(Math.min(...episodes)).padStart(2, '0')}-E${String(Math.max(...episodes)).padStart(2, '0')}`
      : `E${String(episodes[0]).padStart(2, '0')}`
    : '';
  return {
    media,
    title: media?.title || (source ? source.name : task.name),
    subtitle: source ? source.title : task.name,
    seasonEpisode: season
      ? `S${String(season).padStart(2, '0')}${episodeText}`
      : sourceMedia?.episode
        ? `${sourceMedia.season} ${sourceMedia.episode}`
        : source?.season_episode || '',
  };
}

// Match the original card's remaining-time field: unknown estimates stay empty.
export function downloadTimeLeft(task: any) {
  if (typeof task.source_torrent?.left_time === 'string') return task.source_torrent.left_time;
  const speed = Number(task.download_speed || 0);
  const remaining = Math.max(0, Number(task.total_size || 0) - Number(task.completed || 0));
  if (speed <= 0 || remaining <= 0) return '';
  const seconds = Math.floor(remaining / speed);
  if (seconds < 60) return `${seconds}秒`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}分${seconds % 60}秒`;
  if (seconds < 86400)
    return `${Math.floor(seconds / 3600)}小时${Math.floor((seconds % 3600) / 60)}分`;
  return `${Math.floor(seconds / 86400)}天${Math.floor((seconds % 86400) / 3600)}小时`;
}

export function downloadingTaskIsRunning(task: any) {
  return ['downloading', 'queued', 'checking'].includes(task.status);
}

// DownloadingCard uses formatFileSize(size || 0), including signed source
// floats. Keep native daemon counters on the existing app formatter.
export function downloadSourceFileSize(value: number | null | undefined) {
  const bytes = value || 0;
  let size = Math.abs(bytes);
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit++;
  }
  return `${bytes < 0 ? '-' : ''}${size.toFixed(2)} ${units[unit]}`;
}

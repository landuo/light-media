export function downloadTaskInfo(data: any, downloaderID: string, task: any) {
  const hash = String(task.hash || '').toLowerCase();
  const records = [...(data?.intents || []), ...(data?.native_downloads || [])];
  const record = records.find(
    (item: any) =>
      item.downloader_id === downloaderID &&
      String(item.hash || '').toLowerCase() === hash &&
      item.state !== 'cancelled',
  );
  const media = record?.media;
  const season = Number(record?.season || 0);
  const episodes: number[] = record?.episodes || [];
  const episodeText = episodes.length
    ? episodes.length > 1
      ? `E${String(Math.min(...episodes)).padStart(2, '0')}-E${String(Math.max(...episodes)).padStart(2, '0')}`
      : `E${String(episodes[0]).padStart(2, '0')}`
    : '';
  return {
    media,
    title: media?.title || task.name,
    subtitle: task.name,
    seasonEpisode: season ? `S${String(season).padStart(2, '0')}${episodeText}` : '',
  };
}

// Match the original card's remaining-time field: unknown estimates stay empty.
export function downloadTimeLeft(task: any) {
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

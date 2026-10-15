type JobSummary = {
  type_label?: string;
  title?: string;
  subject?: string;
  stage?: string;
  source?: string;
  target?: string;
  file_count?: number;
  downloader?: string;
  activity?: string;
  activity_at?: string;
};

const typeLabels: Record<string, string> = {
  native_download: '下载资源',
  download: '下载资源',
  import: '整理文件',
  reorganize_cleanup: '清理重整旧文件',
  scrape: '刮削媒体资料',
  fnos_sync: '同步飞牛影视',
  subscription: '检查订阅资源',
  library_scan: '核对媒体目录',
  asset_check: '核对媒体文件',
};

const fallbackStages: Record<string, string> = {
  queued: '等待执行',
  running: '正在执行，暂无阶段记录',
  retry_wait: '等待下一次重试',
  waiting_paused: '等待自动化恢复',
  completed: '执行完成',
  succeeded: '执行完成',
  failed: '执行失败',
  cancelled: '已取消',
  canceled: '已取消',
};

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

// Business summaries come from the server's associated records. Do not infer a
// media title from opaque job IDs or expose raw payloads as the task's subject.
export function jobQueueInfo(job: { kind?: string; state?: string; summary?: JobSummary }) {
  const summary = job.summary || {};
  const typeLabel = text(summary.type_label) || typeLabels[job.kind || ''] || '后台任务';
  const title = text(summary.title) || typeLabel;
  const subject = text(summary.subject) || '关联资料不可用';
  const fileCount =
    Number.isInteger(summary.file_count) && Number(summary.file_count) > 0
      ? Number(summary.file_count)
      : undefined;
  const downloader = text(summary.downloader);
  return {
    typeLabel,
    title,
    subject,
    showSubject: !title.includes(subject),
    stage: text(summary.stage) || fallbackStages[job.state || ''] || '暂无阶段记录',
    source: text(summary.source),
    target: text(summary.target),
    fileCount,
    downloader,
    extra: [fileCount ? `${fileCount} 个文件` : '', downloader].filter(Boolean).join(' · '),
    activity: text(summary.activity),
    activityAt: text(summary.activity_at),
  };
}

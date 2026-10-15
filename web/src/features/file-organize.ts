export type OrganizeEntry = {
  path: string;
  season: number | string;
  episodes: string;
  parsed?: { original?: { episodes?: number[] } };
  numbering?: { episodes?: number[] };
  identity?: { id: number; media_type: string; title?: string } | null;
  rootID?: string;
  targetPath?: string;
  automatic?: boolean;
  mediaType?: string;
  renaming?: boolean | null;
  scraping?: boolean | null;
  libraryTypeFolder?: boolean | null;
  mappings?: any[];
  plan?: any;
  selectedIDs?: string[];
  job?: any;
  error?: string;
  state?: string;
};

type BatchNumbering = {
  season: number;
  mode: 'keep' | 'offset' | 'sequential';
  offset: number;
  start: number;
};

export function fillBatchNumbering<T extends OrganizeEntry>(
  files: T[],
  selected: string[],
  rule: BatchNumbering,
): T[] {
  if (!Number.isInteger(rule.season) || rule.season < 1 || rule.season > 1000)
    throw new Error('批量季号必须在 1–1000 之间');
  if (rule.mode === 'offset' && (!Number.isInteger(rule.offset) || Math.abs(rule.offset) > 10000))
    throw new Error('集号偏移必须是 -10000–10000 的整数');
  if (
    rule.mode === 'sequential' &&
    (!Number.isInteger(rule.start) || rule.start < 1 || rule.start > 10000)
  )
    throw new Error('起始集必须在 1–10000 之间');
  const chosen = new Set(selected);
  const ordered = files
    .filter((file) => chosen.has(file.path) && !file.job)
    .sort(
      (a, b) =>
        (a.path.split('/').pop() || '').localeCompare(b.path.split('/').pop() || '', 'zh-CN', {
          numeric: true,
        }) || a.path.localeCompare(b.path, 'zh-CN', { numeric: true }),
    );
  const changed = new Map<string, T>();
  ordered.forEach((file, index) => {
    let episodes = file.episodes;
    if (rule.mode !== 'keep') {
      const parsed = file.parsed?.original?.episodes || file.numbering?.episodes || [];
      if (rule.mode === 'offset' && parsed.length !== 1)
        throw new Error(`文件没有唯一可用的原始集号：${file.path}`);
      const number = rule.mode === 'offset' ? parsed[0]! + rule.offset : rule.start + index;
      if (!Number.isInteger(number) || number < 1 || number > 10000)
        throw new Error(`换算后的集号超出范围：${file.path}`);
      episodes = String(number);
    }
    changed.set(file.path, {
      ...file,
      season: rule.season,
      episodes,
      plan: undefined,
      selectedIDs: [],
      error: '',
      state: file.identity ? 'identified' : 'unidentified',
    });
  });
  return files.map((file) => changed.get(file.path) || file);
}

export function importEntryRequest(entry: OrganizeEntry, mode: string) {
  const identity = entry.identity;
  if (!entry.automatic && (!identity?.id || !['movie', 'tv'].includes(identity.media_type)))
    throw new Error('请先明确选择此文件的影视身份');
  const tv = identity?.media_type === 'tv';
  const episode = Number(entry.episodes.trim());
  const mapped =
    tv && !entry.episodes.trim() && entry.mappings?.some((mapping) => mapping.confirmed === true);
  if (
    tv &&
    (!Number.isInteger(Number(entry.season)) ||
      Number(entry.season) < 1 ||
      Number(entry.season) > 1000 ||
      (!mapped &&
        (!/^\d+$/.test(entry.episodes.trim()) ||
          !Number.isInteger(episode) ||
          episode < 1 ||
          episode > 10000)))
  )
    throw new Error('请为此文件填写一个标准季和单集编号');
  return {
    media_type: identity?.media_type || entry.mediaType || '',
    tmdb_id: identity?.id || 0,
    ...(entry.targetPath !== undefined
      ? { target_path: entry.targetPath }
      : { root_id: entry.rootID }),
    mode,
    ...(entry.renaming == null ? {} : { renaming: entry.renaming }),
    ...(entry.scraping == null ? {} : { scraping: entry.scraping }),
    ...(entry.libraryTypeFolder == null ? {} : { library_type_folder: entry.libraryTypeFolder }),
    files: [
      {
        path: entry.path,
        season: tv ? Number(entry.season) : 0,
        episodes: tv && !mapped ? [episode] : [],
      },
    ],
    mappings: entry.mappings || [],
  };
}

type Invoke = (path: string, body: any) => Promise<any>;
function message(error: unknown) {
  return error instanceof Error ? error.message : '操作失败，请查看关联日志';
}

// Each explicit user action only performs its own stage. A preview never submits
// a job, and a completed submission receipt is never replayed by a retry click.
export async function previewOrganizeEntries(
  entries: OrganizeEntry[],
  mode: string,
  invoke: Invoke,
) {
  for (const entry of entries) {
    if (entry.job) continue;
    entry.error = '';
    entry.state = 'previewing';
    entry.plan = undefined;
    entry.selectedIDs = [];
    try {
      const plan = await invoke('/import/preview', importEntryRequest(entry, mode));
      if (!plan?.id || !Array.isArray(plan.files)) throw new Error('服务未返回有效整理预览');
      entry.plan = plan;
      entry.selectedIDs = plan.files
        .filter((file: any) => ['ready', 'already_imported'].includes(file.status))
        .map((file: any) => file.id);
      entry.state = entry.selectedIDs!.length ? 'ready' : 'blocked';
      if (!entry.selectedIDs!.length)
        entry.error =
          plan.files
            .map((file: any) => file.reason)
            .filter(Boolean)
            .join('；') || '该文件尚不能执行，请核对识别结果';
    } catch (e) {
      entry.state = 'failed';
      entry.error = message(e);
    }
  }
  const targets = new Map<string, OrganizeEntry>();
  for (const entry of entries.filter((item) => !item.job && item.plan))
    for (const file of entry.plan.files || []) {
      if (!file.target || !['ready', 'already_imported'].includes(file.status)) continue;
      const previous = targets.get(file.target);
      if (previous && previous !== entry)
        for (const conflict of [entry, previous]) {
          conflict.state = 'blocked';
          conflict.selectedIDs = [];
          conflict.error = '多个文件使用同一个目标路径，请取消重复项或修正身份后重新预览';
        }
      else targets.set(file.target, entry);
    }
}

export async function confirmOrganizeEntries(entries: OrganizeEntry[], invoke: Invoke) {
  for (const entry of entries) {
    if (
      entry.job ||
      !entry.plan?.id ||
      !entry.selectedIDs?.length ||
      !['ready', 'submit_failed'].includes(entry.state || '')
    )
      continue;
    entry.error = '';
    entry.state = 'submitting';
    try {
      const job = await invoke('/import/execute', {
        plan_id: entry.plan.id,
        file_ids: entry.selectedIDs,
      });
      if (!job?.id) throw new Error('服务未返回整理任务，提交结果待核对');
      entry.job = job;
      entry.state = 'queued';
    } catch (e) {
      entry.state = 'submit_failed';
      entry.error = message(e);
    }
  }
}

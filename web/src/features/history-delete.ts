import type { HistoryRow } from './transfer-history.ts';

export interface HistoryDeleteRequest {
  confirm: true;
  file_id: string;
  delete_source: boolean;
  delete_target: boolean;
}

export async function deleteHistoryBatch(
  items: HistoryRow[],
  deleteSource: boolean,
  deleteTarget: boolean,
  remove: (item: HistoryRow, request: HistoryDeleteRequest) => Promise<{ deleted?: boolean }>,
  progress: (item: HistoryRow, done: number, total: number) => void,
) {
  const succeeded: HistoryRow[] = [];
  const failed: { item: HistoryRow; error: unknown }[] = [];
  for (const [index, item] of items.entries()) {
    progress(item, index, items.length);
    try {
      const result = await remove(item, {
        confirm: true,
        file_id: item.file_id || item.id,
        delete_source: deleteSource,
        delete_target: deleteTarget,
      });
      if (result?.deleted !== true) throw new Error('服务未确认删除完成，请刷新后核对');
      succeeded.push(item);
    } catch (error) {
      failed.push({ item, error });
    }
    progress(item, index + 1, items.length);
  }
  return { succeeded, failed };
}

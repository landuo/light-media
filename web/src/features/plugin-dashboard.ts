import type { PluginFormNode } from './plugin-forms';
export type DashboardMeta = { id: string; key: string; name: string };
export type DashboardItem = DashboardMeta & {
  cols?: Record<string, unknown>;
  attrs?: Record<string, any>;
  rows?: unknown;
  elements?: PluginFormNode[];
  render_mode?: string;
};
export type LayoutItem = { x: number; y: number; w: number; h?: number };
export type DashboardLayout = {
  items: Record<string, LayoutItem>;
  enabled?: Record<string, boolean>;
  updatedAt?: number;
};
export type DashboardProfile = 'desktop' | 'tablet' | 'mobile';
export function dashboardID(item: Pick<DashboardMeta, 'id' | 'key'>) {
  return item.key ? item.id + ':' + item.key : item.id;
}
export function dashboardProfile(width: number): DashboardProfile {
  return width <= 640 ? 'mobile' : width <= 960 ? 'tablet' : 'desktop';
}
export function dashboardColumns(profile: DashboardProfile) {
  return profile === 'mobile' ? 1 : profile === 'tablet' ? 6 : 12;
}
export function dashboardInt(value: unknown, min: number, max: number, fallback: number) {
  const number = Number(value);
  return Number.isFinite(number) ? Math.min(max, Math.max(min, Math.round(number))) : fallback;
}
function object(value: unknown): value is Record<string, any> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}
export function dashboardLayout(value: unknown): DashboardLayout | undefined {
  if (!object(value)) return;
  const wrapped = Object.hasOwn(value, 'items') || Object.hasOwn(value, 'enabled');
  const input = wrapped ? (value.items ?? {}) : value;
  const items: Record<string, LayoutItem> = Object.create(null);
  if (object(input))
    for (const [id, item] of Object.entries(input)) {
      if (!object(item)) continue;
      const w = dashboardInt(item.w, 1, 12, 12);
      const entry: LayoutItem = {
        x: dashboardInt(item.x, 0, 12 - w, 0),
        y: dashboardInt(item.y, 0, 999, 0),
        w,
      };
      if (item.h !== undefined) entry.h = dashboardInt(item.h, 1, 96, 4);
      items[id] = entry;
    }
  else if (!wrapped) return;
  const out: DashboardLayout = { items };
  if (object(value.enabled))
    out.enabled = Object.fromEntries(
      Object.entries(value.enabled).map(([id, enabled]) => [id, Boolean(enabled)]),
    );
  const timestamp = Number(value.updatedAt);
  if (Number.isFinite(timestamp) && timestamp > 0) out.updatedAt = timestamp;
  return out;
}
export function dashboardWidget(
  item: DashboardItem,
  profile: DashboardProfile,
  layout: DashboardLayout,
  autoHeight?: number,
) {
  const id = dashboardID(item),
    columns = dashboardColumns(profile),
    saved = layout.items[id];
  const declared =
    profile === 'mobile'
      ? 1
      : profile === 'tablet'
        ? (item.cols?.sm ?? item.cols?.md)
        : (item.cols?.md ?? item.cols?.cols);
  const w = dashboardInt(saved?.w ?? declared, 1, columns, columns);
  return {
    id,
    w,
    h: dashboardInt(saved?.h ?? autoHeight ?? item.rows ?? item.attrs?.rows, 1, 96, 4),
    minW: 1,
    minH: 1,
    ...(saved
      ? { x: dashboardInt(saved.x, 0, columns - w, 0), y: saved.y }
      : { autoPosition: true }),
  };
}
// Keep the installed JavaScript's truth/multiplication semantics. A negative
// value or the truthy string "0" is an immediate timer; neither is clamped to
// an invented minimum. Disabled widgets never schedule or poll.
export function dashboardRefresh(item: DashboardItem, enabled: boolean, active: boolean) {
  return enabled && active && item.attrs?.refresh ? 1000 * item.attrs.refresh : undefined;
}

// Responses replace the declaration/data. A refresh carries only metadata;
// spreading the previous full widget here would silently retain stale rows.
export function dashboardResponse(meta: DashboardMeta, result: Record<string, any>): DashboardItem {
  return { ...result, id: meta.id, key: meta.key, name: meta.name };
}

export function dashboardEnabled(value: unknown): Record<string, boolean> | undefined {
  if (!object(value)) return;
  return Object.fromEntries(Object.entries(value).map(([id, enabled]) => [id, Boolean(enabled)]));
}
// The installed page keeps a newer local edit, backfills an absent server row,
// and retains local display flags when an old server envelope lacks them.
export function dashboardPreferredLayout(
  local: DashboardLayout | undefined,
  remote: DashboardLayout | undefined,
) {
  if (!remote) return { layout: local, write: local !== undefined };
  if (
    local?.updatedAt !== undefined &&
    (remote.updatedAt === undefined || local.updatedAt > remote.updatedAt)
  )
    return { layout: local, write: true };
  const layout: DashboardLayout = { ...remote };
  if (layout.enabled === undefined && local?.enabled !== undefined)
    layout.enabled = { ...local.enabled };
  return { layout, write: remote.enabled === undefined && local?.enabled !== undefined };
}

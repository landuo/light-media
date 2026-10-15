export type BrowserRoot = { id: string; path: string; name: string; kind?: string };
export type BrowserItem = {
  name: string;
  path: string;
  type: string;
  size?: number;
  modified?: string;
  video?: boolean;
  protected?: string;
  virtual?: boolean;
};

export function isSelectableBrowserItem(item: BrowserItem) {
  return !item.virtual && ['dir', 'file'].includes(item.type);
}

export function retainBrowserSelection(selected: string[], items: BrowserItem[]) {
  const available = new Set(items.filter(isSelectableBrowserItem).map((item) => item.path));
  return [...new Set(selected)].filter((path) => available.has(path));
}

export function cleanBrowserPath(path: string) {
  return path.replace(/\/+$/, '') || '/';
}
export function browserRootForPath(roots: BrowserRoot[], path: string) {
  const value = cleanBrowserPath(path);
  return [...roots]
    .filter((root) => value === root.path || value.startsWith(root.path.replace(/\/$/, '') + '/'))
    .sort((a, b) => b.path.length - a.path.length)[0];
}

// The local-storage root exposes only mounted, authorized folders. Its parent
// segments are navigation nodes, never capabilities for file operations.
export function virtualBrowserChildren(roots: BrowserRoot[], path: string): BrowserItem[] {
  const prefix = path === '/' ? '/' : cleanBrowserPath(path) + '/';
  const children = new Map<string, BrowserItem>();
  for (const root of roots) {
    if (!root.path.startsWith(prefix)) continue;
    const name = root.path.slice(prefix.length).split('/')[0];
    if (!name) continue;
    const child = prefix + name;
    children.set(child, { name, path: child, type: 'dir', virtual: true });
  }
  return [...children.values()].sort((a, b) =>
    a.name.localeCompare(b.name, 'zh-CN', { numeric: true }),
  );
}

export function commonDownloadPath(paths: string[]) {
  const values = paths
    .filter(Boolean)
    .map((path) => cleanBrowserPath(path).split('/').filter(Boolean));
  if (!values.length) return '/';
  let length = 0;
  while (
    length < values[0]!.length &&
    values.every((value) => value[length] === values[0]![length])
  )
    length++;
  return '/' + values[0]!.slice(0, length).join('/');
}

export function filterBrowserItems<T extends BrowserItem>(
  items: T[],
  filter: string,
  ignoreCase: boolean,
): T[] {
  if (!filter) return items;
  if (filter.includes('*') || filter.includes('?')) {
    const escaped = filter
      .replace(/[.+^${}()|[\]\\]/g, '\\$&')
      .replace(/\*/g, '.*')
      .replace(/\?/g, '.');
    const pattern = new RegExp('^' + escaped + '$', ignoreCase ? 'i' : '');
    return items.filter((item) => pattern.test(item.name));
  }
  const search = ignoreCase ? filter.toLowerCase() : filter;
  return items.filter((item) =>
    (ignoreCase ? item.name.toLowerCase() : item.name).includes(search),
  );
}

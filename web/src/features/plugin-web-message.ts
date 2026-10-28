import MarkdownIt from 'markdown-it';
import linkAttributes from 'markdown-it-link-attributes';
export type PluginWebMessage = {
  action?: number | null;
  userid?: string | number | null;
  title?: string | null;
  text?: string | null;
  reg_time?: string | null;
  date?: string | null;
  note?: unknown;
  image?: string | null;
  link?: string | null;
  [field: string]: unknown;
};
const markdown = new MarkdownIt({ html: true, breaks: true, linkify: true, typographer: true });
markdown.use(linkAttributes, { attrs: { target: '_blank', rel: 'noopener noreferrer' } });
export function renderPluginWebMessage(value: string) {
  return value ? markdown.render(value) : '';
}
export function pluginMessageNote(note: unknown): string {
  if (note == null) return '';
  if (typeof note === 'string') return note;
  if (typeof note === 'object' && !Array.isArray(note) && Object.keys(note).length === 0) return '';
  return JSON.stringify(note);
}
export function pluginMessageNoteItems(note: unknown): any {
  if (!note) return {};
  if (typeof note !== 'string') return note;
  try {
    return JSON.parse(note);
  } catch {
    return note;
  }
}
export function pluginMessageHasNote(note: unknown) {
  return note != null && !(typeof note === 'object' && Object.keys(note).length === 0);
}
export function pluginMessageKey(message: PluginWebMessage) {
  return [
    message.action ?? '',
    message.userid ?? '',
    message.reg_time || message.date || '',
    message.title ?? '',
    message.text ?? '',
    message.image ?? '',
    message.link ?? '',
    pluginMessageNote(message.note),
  ].join('::');
}
export function pluginMessageTime(message: PluginWebMessage) {
  return message.reg_time || message.date || '';
}
export function pluginMessageCompareTime(first: string, second: string) {
  if (!first && !second) return 0;
  if (!first) return -1;
  if (!second) return 1;
  const parse = (value: string) =>
    new Date(value.includes('T') ? value : value.replaceAll('-', '/')).getTime();
  return parse(first) - parse(second);
}
export function pluginWebMessages(value: unknown): PluginWebMessage[] {
  if (
    !Array.isArray(value) ||
    value.some((item) => !item || typeof item !== 'object' || Array.isArray(item))
  )
    throw new Error('消息历史返回格式无效');
  // The pinned MessageView sorts both incoming batches and the merged list
  // after appending unseen signatures.
  return [...value].sort((a, b) =>
    pluginMessageCompareTime(pluginMessageTime(a), pluginMessageTime(b)),
  );
}

// The pinned shortcut helper scans the full URL without decoding the value.
export function pluginMessageShortcut(url: string) {
  return /[?&]shortcut=([^&#]*)/i.exec(url)?.[1] === 'message';
}

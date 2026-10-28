import MarkdownIt from 'markdown-it';
import linkAttributes from 'markdown-it-link-attributes';

// Pinned VersionHistory.vue uses these renderer options and link attributes.
const markdown = new MarkdownIt({ html: true, linkify: true, typographer: true });
markdown.use(linkAttributes, { attrs: { target: '_blank', rel: 'noopener noreferrer' } });

export function renderPluginHistory(note: string): string {
  return note ? markdown.render(note) : '';
}

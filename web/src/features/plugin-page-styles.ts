// Fixed FormRender/PageRender/DashboardRender pass stylesheet text to a
// native style node. Installed plugin code is already trusted executable code;
// a CSS property/selector allowlist would silently change supported rendering.
// DOM text children preserve the text without parsing it as HTML.
export function pluginPageStyles(text: unknown, _scope = 'validation'): string | null {
  return text == null ? '' : typeof text === 'string' ? text : typeof text === 'number' ? String(text) : null;
}

import { parse, type ChildNode, type Container } from 'postcss';

// Deliberately a presentation subset, not an arbitrary stylesheet capability.
// Parse first, validate every AST node, and emit only rebuilt approved rules.
const properties = new Set([
  'display',
  'flex-direction',
  'gap',
  'grid-template-columns',
  'min-width',
  'max-width',
  'width',
  'height',
  'min-height',
  'padding',
  'padding-left',
  'padding-right',
  'margin-top',
  'margin-bottom',
  'align-items',
  'justify-content',
  'color',
  'font-size',
  'font-weight',
  'line-height',
  'overflow',
  'overflow-x',
  'text-overflow',
  'white-space',
  'letter-spacing',
  'border',
  'border-radius',
  'border-bottom',
  'border-color',
  'background',
  'background-color',
  'backdrop-filter',
  'vertical-align',
  'text-align',
  'opacity',
]);
const functions = new Set(['rgb', 'rgba', 'var', 'repeat', 'minmax', 'blur']);
const tags = new Set(['div', 'span', 'table', 'thead', 'tbody', 'tr', 'th', 'td']);

function selector(raw: string, scope: string): string {
  let value = raw.trim();
  let ancestor = '';
  for (const theme of ['html[data-theme="transparent"]', '.v-theme--transparent']) {
    if (value.startsWith(theme + ' ')) {
      ancestor = theme + ' ';
      value = value.slice(theme.length).trim();
      break;
    }
  }
  const parts = value.split(/\s+/);
  if (
    !parts.length ||
    parts.some((part) => {
      const plain = part.replace(/:last-child$/, '');
      return !(tags.has(plain) || /^\.[A-Za-z_][A-Za-z0-9_-]*$/.test(plain));
    })
  )
    throw new Error('unsupported selector');
  return `${ancestor}[data-plugin-page-scope="${scope}"] ${value}`;
}

function singleFunction(value: string, names: string[]): boolean {
  const text = value.trim();
  const open = text.indexOf('(');
  if (open < 0 || !names.includes(text.slice(0, open))) return false;
  let depth = 0;
  for (let i = open; i < text.length; i++) {
    if (text[i] === '(') depth++;
    else if (text[i] === ')') depth--;
    if (depth === 0 && i !== text.length - 1) return false;
    if (depth < 0) return false;
  }
  return depth === 0;
}

function declaration(node: ChildNode): string {
  if (node.type !== 'decl' || !properties.has(node.prop))
    throw new Error('unsupported declaration');
  const value = node.value;
  if (!value || value.length > 256 || !/^[A-Za-z0-9_#.,%()\s-]+$/.test(value))
    throw new Error('unsupported value');
  for (const match of value.matchAll(/([A-Za-z_-][A-Za-z0-9_-]*)\s*\(/g)) {
    if (!functions.has(match[1])) throw new Error('unsupported function');
  }
  // Only existing theme variables, never author-defined or resource-bearing variables.
  for (const match of value.matchAll(/var\(\s*([^,)\s]+)/g)) {
    if (
      !/^--(?:v-theme-[a-z-]+|v-medium-emphasis-opacity|app-card-accent-rgb|transparent-blur)$/.test(
        match[1],
      )
    )
      throw new Error('unsupported variable');
  }
  // A bare variable in image/filter-capable properties could resolve to a
  // resource function at computed-value time. Keep variables inside a color
  // or blur function where image/filter URLs cannot become valid values.
  if (
    ['background', 'background-color'].includes(node.prop) &&
    value !== 'transparent' &&
    !/^#[0-9a-fA-F]{3,8}$/.test(value) &&
    !singleFunction(value, ['rgb', 'rgba'])
  )
    throw new Error('resource-capable background');
  if (node.prop === 'backdrop-filter' && !singleFunction(value, ['blur']))
    throw new Error('resource-capable filter');
  return `${node.prop}:${value}${node.important ? ' !important' : ''};`;
}

export function pluginPageStyles(text: unknown, scope = 'validation'): string | null {
  if (typeof text !== 'string' || text.length > 32768 || !/^[A-Za-z0-9_-]{1,80}$/.test(scope))
    return null;
  try {
    const root = parse(text);
    let count = 0;
    const render = (container: Container, depth: number): string => {
      if (depth > 1) throw new Error('nested media');
      return (container.nodes || [])
        .map((node) => {
          if (++count > 512) throw new Error('stylesheet limit');
          if (node.type === 'comment') throw new Error('comments not supported');
          if (node.type === 'atrule') {
            if (
              depth ||
              node.name !== 'media' ||
              !/^\((?:min|max)-width:\s*[0-9]{1,4}px\)$/.test(node.params)
            )
              throw new Error('unsupported at-rule');
            return `@media ${node.params}{${render(node, depth + 1)}}`;
          }
          if (node.type !== 'rule' || !node.nodes?.length) throw new Error('unsupported rule');
          const selectors = node.selector.split(',').map((s) => selector(s, scope));
          if (selectors.length > 32) throw new Error('selector limit');
          return `${selectors.join(',')}{${node.nodes.map(declaration).join('')}}`;
        })
        .join('');
    };
    return render(root, 0);
  } catch {
    return null;
  }
}

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pluginPageIssues, pluginFormIssues } from '../src/features/plugin-forms.ts';
import { pluginPageStyles } from '../src/features/plugin-page-styles.ts';

const page = JSON.parse(
  readFileSync(new URL('./fixtures/plugin-page-original.json', import.meta.url), 'utf8'),
);
test('complete real original page is supported without dropping style, spacer or chip', () => {
  assert.deepEqual(pluginPageIssues(page), []);
  const css = pluginPageStyles(page[0].text, 'instance-one');
  assert.ok(css);
  assert.match(css, /\[data-plugin-page-scope="instance-one"\] \.autosignin-summary/);
  assert.match(css, /@media \(max-width: 720px\)/);
  assert.match(css, /html\[data-theme="transparent"\] \[data-plugin-page-scope="instance-one"\]/);
  assert.ok(!css.includes('instance-two'));
  assert.ok(pluginFormIssues([page[0]]).length);
});
test('stylesheet rejects networking, global escape, active content and unapproved syntax', () => {
  for (const css of [
    '.x { background:var(--v-theme-surface); }',
    '.x { background:rgb(0,0,0) var(--v-theme-surface); }',
    '.x { background-color:var(--v-theme-surface); }',
    '.x { backdrop-filter:var(--transparent-blur); }',
    '.x { backdrop-filter:blur(1px) var(--transparent-blur); }',
    '.x { background-image:var(--v-theme-surface); }',
    '.x { mask:var(--v-theme-surface); }',
    '.x { cursor:var(--v-theme-surface); }',
    '.x { list-style-image:var(--v-theme-surface); }',
    '.x { content:var(--v-theme-surface); }',
    '.x { filter:var(--v-theme-surface); }',
    '.x { border-image:var(--v-theme-surface); }',
    '.x { src:var(--v-theme-surface); }',
    '@import "https://example.invalid/a.css";',
    '.x { background: url(data:image/svg+xml,x); }',
    '.x { color: var(--external-resource); }',
    'html[data-theme="transparent"] body { color:red; }',
    '.x > body { color:red; }',
    ':is(.x, body) { color:red; }',
    '[data-plugin-page-scope] { color:red; }',
    '.x\\,body { color:red; }',
    '.x { background: url(https://example.invalid/pixel); }',
    '.x { background: URL(https://example.invalid/pixel); }',
    '.x { background: image-set(url(x)); }',
    'body { color: red; }',
    ':root { color:red; }',
    '.x, body { color: red; }',
    '.x ~ .outside { color:red; }',
    '.x:has(input[value]) { color:red; }',
    '.x { position:fixed; }',
    '.x { --payload:red; }',
    '.x { color:var(--payload); }',
    '.x { color:expression(alert(1)); }',
    '.x { color: r\\65 d; }',
    '@font-face { font-family:x;src:url(x); }',
    '@supports (display:grid) { .x { color:red; } }',
    '@media (max-width:720px) { @media (min-width:1px) { .x {color:red;} } }',
    '.x { .y { color:red; } }',
    '.x { color:red; } </style><script>x</script>',
  ])
    assert.equal(pluginPageStyles(css, 'safe'), null, css);
  assert.equal(pluginPageStyles('.x {color:red}', 'x"] body'), null);
  assert.equal(pluginPageStyles('x'.repeat(32769)), null);
  assert.ok(
    pluginPageIssues([{ component: 'style', text: '.x {color:red}', props: { media: 'print' } }])
      .length,
  );
  assert.ok(pluginPageIssues([{ component: 'style', text: '.x {color:red}', content: [] }]).length);
  assert.ok(pluginPageIssues([{ component: 'UnknownRemoteWidget' }]).length);
  assert.ok(
    pluginPageIssues([{ component: 'VChip', props: { href: 'https://example.invalid' } }]).length,
  );
  assert.ok(
    pluginPageIssues([{ component: 'VSpacer', props: { style: 'position:fixed' } }]).length,
  );
});
test('renderer registers actual components and binds style only to the instance page scope', () => {
  const renderer = readFileSync(
    new URL('../src/components/PluginFormRenderer.vue', import.meta.url),
    'utf8',
  );
  const dialog = readFileSync(
    new URL('../src/components/PluginDataDialog.vue', import.meta.url),
    'utf8',
  );
  assert.match(renderer, /import \{ VChip \} from 'vuetify\/components\/VChip'/);
  assert.match(renderer, /import \{ VRow, VCol, VSpacer \} from 'vuetify\/components\/VGrid'/);
  assert.match(
    renderer,
    /props.pageScope \? pluginPageStyles\(node.text, props.pageScope\) : null/,
  );
  assert.match(dialog, /:data-plugin-page-scope="pageScope"/);
  assert.match(dialog, /:page-scope="pageScope"/);
  assert.doesNotMatch(renderer + dialog, /AutoSignIn/);
});

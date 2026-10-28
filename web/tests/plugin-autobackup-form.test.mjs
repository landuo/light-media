import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  pluginFormIssues,
  pluginNodeVisible,
  pluginFieldProps,
} from '../src/features/plugin-forms.ts';

const form = JSON.parse(
  readFileSync(new URL('../../internal/plugins/autobackup/form.json', import.meta.url), 'utf8'),
);
test('complete original AutoBackup form supports subtitle and six exact visibility declarations', () => {
  assert.deepEqual(pluginFormIssues(form.schema), []);
  const visibility = [];
  const subtitles = [];
  function walk(nodes) {
    for (const node of nodes) {
      if (Object.hasOwn(node.props || {}, 'show')) visibility.push(node);
      if (node.component === 'VSubheader') subtitles.push(node);
      walk(node.content || []);
    }
  }
  walk(form.schema);
  assert.equal(visibility.length, 6);
  assert.deepEqual(
    subtitles.map((n) => n.props.text),
    ['WebDAV备份配置'],
  );
  const model = { webdav_enabled: false, webdav_password: 'synthetic-value' };
  for (const node of visibility) {
    assert.equal(node.props.show, '{{webdav_enabled}}');
    assert.equal(pluginNodeVisible(node.props, model), false);
    assert.equal(Object.hasOwn(pluginFieldProps(node.props, model, node.component), 'show'), false);
  }
  model.webdav_enabled = true;
  for (const node of visibility) assert.equal(pluginNodeVisible(node.props, model), true);
  model.webdav_enabled = false;
  assert.equal(model.webdav_password, 'synthetic-value');
});
test('visibility preserves fixed with-model JavaScript truthiness and missing-name errors', () => {
  for (const [value, expected] of [
    [false, false],
    [0, false],
    ['', false],
    [[], true],
    [null, false],
    [undefined, false],
    [true, true],
    ['false', true],
  ]) {
    assert.equal(pluginNodeVisible({ show: '{{enabled}}' }, { enabled: value }), expected);
    // Original FormRender parseProps uses JavaScript truthiness, not Python's.
    assert.equal(Boolean(value), expected);
  }
  const model = Object.create({ inherited: true });
  model['plain_key'] = true;
  assert.equal(pluginNodeVisible({ show: '{{ plain_key }}' }, model), true);
  assert.equal(pluginNodeVisible({ show: '{{ inherited }}' }, model), true); // Fixed with(model) reads inherited names.
  assert.throws(() => pluginNodeVisible({ show: '{{ missing }}' }, model), ReferenceError);
  assert.equal(pluginNodeVisible({}, model), true);
  const draft = { enabled: false, a: 3, b: 2, other: true };
  for (const show of ['{{!enabled}}', '{{a || b}}', '{{model.other}}', '{{a-b}}', 'true']) {
    assert.equal(pluginNodeVisible({ show }, draft), true);
    assert.deepEqual(pluginFormIssues([{ component: 'VRow', props: { show } }]), []);
  }
  assert.throws(() => pluginNodeVisible({ show: '{{missing + 1}}' }, draft), /missing/);
  for (const show of ['{{a + }}', true]) {
    assert.ok(pluginFormIssues([{ component: 'VRow', props: { show } }]).length);
  }
  assert.ok(
    pluginFormIssues([{ component: 'VSubheader', props: { text: 'x', innerHTML: '<b>x</b>' } }])
      .length,
  );
});
test('renderer maps subtitle to actual Vuetify and applies visibility without a plugin special case', () => {
  const source = readFileSync(
    new URL('../src/components/PluginFormRenderer.vue', import.meta.url),
    'utf8',
  );
  assert.match(source, /VSubheader: VListSubheader/);
  assert.match(source, /bound.title = bound.text/);
  assert.equal(pluginFieldProps({ show: '{{ visible }}' }, { visible: false }).style.display, 'none');
  assert.equal(pluginFieldProps({ show: '{{ visible }}', style: { color: 'red' } }, { visible: false }).style.display, undefined, 'fixed source later style overwrites earlier show style');
  assert.doesNotMatch(source, /AutoBackup|webdav_enabled/);
});

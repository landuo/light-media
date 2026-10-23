import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pluginFormIssues } from '../src/features/plugin-forms.ts';

// Read the complete frozen source form, never a reduced synthetic mock.
const form = JSON.parse(
  readFileSync(new URL('../../internal/plugins/removelink/form.json', import.meta.url), 'utf8'),
);
test('complete original RemoveLink form is supported with all thirteen model fields', () => {
  assert.deepEqual(pluginFormIssues(form.conf), []);
  const fields = new Set();
  let dividers = 0;
  function walk(nodes) {
    for (const node of nodes) {
      if (node.component === 'VDivider') {
        dividers++;
        assert.deepEqual(node.props, { style: 'margin: 20px 0;' });
      }
      for (const key of ['model', 'modelvalue', 'v-model']) {
        if (node.props?.[key]) fields.add(node.props[key]);
      }
      if (node.content) walk(node.content);
    }
  }
  walk(form.conf);
  assert.equal(dividers, 3);
  assert.equal(fields.size, 13);
  assert.deepEqual([...fields].sort(), Object.keys(form.model).sort());
});
test('generic renderer binds the actual Vuetify divider without a plugin special case', () => {
  const source = readFileSync(
    new URL('../src/components/PluginFormRenderer.vue', import.meta.url),
    'utf8',
  );
  assert.match(source, /import \{ VDivider \} from 'vuetify\/components\/VDivider'/);
  assert.match(source, /\n  VDivider,/);
  assert.doesNotMatch(source, /RemoveLink/);
});

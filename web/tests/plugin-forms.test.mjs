import test from 'node:test';
import assert from 'node:assert/strict';
import {
  pluginFormIssues,
  pluginPageIssues,
  pluginFieldProps,
} from '../src/features/plugin-forms.ts';

test('original switch and Ace model bindings save the current independent draft', () => {
  const model = { enabled: false, content: 'movie:\n' };
  const switchProps = pluginFieldProps(
    { model: 'enabled', label: '启用插件（写入配置文件）' },
    model,
  );
  const editorProps = pluginFieldProps(
    { modelvalue: 'content', lang: 'yaml', theme: 'monokai' },
    model,
  );
  assert.equal(switchProps.modelValue, false);
  assert.equal(editorProps.value, 'movie:\n');
  switchProps['onUpdate:modelValue'](true);
  editorProps['onUpdate:value']('movie:\n  华语电影:\n');
  assert.deepEqual(model, { enabled: true, content: 'movie:\n  华语电影:\n' });
  assert.equal(editorProps.theme, 'monokai');
});

test('plugin data tables support actual process lists and reject executable HTML', () => {
  const table = [
    {
      component: 'VTable',
      content: [
        {
          component: 'tbody',
          content: Array.from({ length: 1024 }, (_, i) => ({
            component: 'tr',
            content: [{ component: 'td', text: i }],
          })),
        },
      ],
    },
  ];
  assert.deepEqual(pluginPageIssues(table), []);
  assert.notDeepEqual(pluginFormIssues(table), []);
  for (const node of [
    { component: 'script', text: 'alert(1)' },
    { component: 'td', props: { innerHTML: '<img src=x onerror=alert(1)>' } },
    { component: 'VSwitch', props: { model: 'constructor' } },
  ])
    assert.notDeepEqual(pluginPageIssues([node]), []);
  assert.equal('innerHTML' in pluginFieldProps({ innerHTML: '<b>unsafe</b>' }, {}), false);
});

test('unsupported original UI behavior is reported before save, not silently discarded', () => {
  assert.deepEqual(
    pluginFormIssues([
      {
        component: 'VForm',
        content: [
          { component: 'VSwitch', props: { model: 'enabled' } },
          { component: 'VAceEditor', props: { modelvalue: 'content' } },
        ],
      },
    ]),
    [],
  );
  assert.equal(pluginFormIssues([{ component: 'UnportedBusinessComponent' }]).length, 1);
  assert.equal(
    pluginFormIssues([
      { component: 'VSwitch', props: { model: 'enabled', show: '{{ model.other }}' } },
    ]).length,
    1,
  );
  assert.equal(
    pluginFormIssues([{ component: 'VSwitch', props: { model: '__proto__' } }]).length,
    1,
  );
  assert.equal(
    pluginFormIssues([{ component: 'VForm', props: { onClick: 'unportedAction()' } }]).length,
    1,
  );
});

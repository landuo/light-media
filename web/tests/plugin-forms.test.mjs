import test from 'node:test';
import assert from 'node:assert/strict';
import { pluginFormIssues, pluginFieldProps } from '../src/features/plugin-forms.ts';

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

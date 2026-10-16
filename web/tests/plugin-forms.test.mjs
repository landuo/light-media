import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  pluginFormIssues,
  pluginPageIssues,
  pluginFieldProps,
  validPluginPieChart,
  pluginChartHTML,
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

test('all installed SiteStatistic reference pages and charts render through the whitelist', () => {
  const fixture = JSON.parse(
    readFileSync(
      new URL('../../testdata/plugins/site-statistic-original-oracle.json', import.meta.url),
      'utf8',
    ),
  );
  assert.deepEqual(pluginFormIssues(fixture.form), []);
  for (const item of fixture.cases) {
    assert.deepEqual(pluginPageIssues(item.page), [], item.name);
    for (const [mode, dashboard] of Object.entries(item.dashboards))
      assert.deepEqual(pluginPageIssues(dashboard.elements), [], item.name + ':' + mode);
  }
  const chart = fixture.cases.find((item) => item.name === 'rounded-chart-values').dashboards.today
    .elements[0].content[0].content[0];
  assert.equal(validPluginPieChart(chart.props), true);
  for (const mutation of [
    {
      ...chart.props,
      options: { ...chart.props.options, tooltip: { custom: '<script>bad</script>' } },
    },
    {
      ...chart.props,
      options: { ...chart.props.options, chart: { type: 'pie', events: { click: 'bad()' } } },
    },
    { ...chart.props, series: [Infinity] },
    { ...chart.props, series: [-1] },
  ])
    assert.equal(validPluginPieChart(mutation), false);
  assert.equal(
    pluginChartHTML('<img src=x onerror="bad()"> & site'),
    '&lt;img src=x onerror=&quot;bad()&quot;&gt; &amp; site',
  );
  for (const src of [
    'https://other.invalid/logo.png',
    '/plugin_icon/../../evil.png',
    'data:text/html,<script>bad()</script>',
  ])
    assert.notDeepEqual(pluginPageIssues([{ component: 'VImg', props: { src } }]), []);
});

test('all actual MPServerStatus forms and complete reference pages use supported original components', () => {
  const fixture = JSON.parse(
    readFileSync(
      new URL('../../testdata/plugins/server-status-original-oracle.json', import.meta.url),
      'utf8',
    ),
  );
  let nodeTrees = 0;
  const visit = (value) => {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      if (value.length && value.every((item) => typeof item?.component === 'string')) {
        assert.deepEqual(pluginPageIssues(value), []);
        nodeTrees++;
      }
      for (const item of value) visit(item);
    } else {
      for (const item of Object.values(value)) visit(item);
    }
  };
  for (const item of fixture.cases) visit(item.expected);
  assert.ok(nodeTrees > 100, 'complete original render trees were not exercised');
  for (const item of fixture.cases.filter((item) => item.kind === 'form' && item.expected?.conf))
    assert.deepEqual(pluginFormIssues(item.expected.conf), []);
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

test('complete large plugin histories retain every row with bounded page rendering', () => {
  const history = (count) => [
    {
      component: 'VTable',
      content: [
        {
          component: 'tbody',
          content: Array.from({ length: count }, (_, i) => ({
            component: 'tr',
            content: ['name', 'type', 'content', 'time'].map((field) => ({
              component: 'td',
              text: `${field} ${i}`,
            })),
          })),
        },
      ],
    },
  ];
  assert.deepEqual(pluginPageIssues(history(4500)), []);
  assert.deepEqual(pluginPageIssues(history(14000)), ['插件页面结构无效或超过显示限制']);
  assert.notDeepEqual(pluginFormIssues(history(4500)), []);
  const nested = { component: 'div' };
  let parent = nested;
  for (let i = 0; i < 18; i++) {
    const child = { component: 'div' };
    parent.content = [child];
    parent = child;
  }
  assert.notDeepEqual(pluginPageIssues([nested]), []);
});

test('original uninstall switches and installed-plugin multiselect use supported controls', () => {
  const form = JSON.parse(
    readFileSync(new URL('../../internal/plugins/uninstall/form.json', import.meta.url), 'utf8'),
  );
  assert.deepEqual(pluginFormIssues(form.conf), []);
});

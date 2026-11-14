import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { reactive, effect, stop } from 'vue';
import {
  pluginFormIssues,
  pluginPageIssues,
  pluginFieldProps,
  validPluginPieChart,
  pluginChartHTML,
  validPluginLink,
} from '../src/features/plugin-forms.ts';

test('full original MergeMessageNotify forms retain literal dotted keys, tabs and nested dialogs', () => {
  const forms = JSON.parse(
    readFileSync(
      new URL('../../internal/plugins/mergemessagenotify/forms.json', import.meta.url),
      'utf8',
    ),
  );
  let count = 0;
  const visit = (nodes) => {
    for (const node of nodes) {
      count++;
      if (node.content) visit(node.content);
    }
  };
  for (const form of Object.values(forms)) {
    assert.deepEqual(pluginFormIssues(form.elements), []);
    visit(form.elements);
  }
  assert.equal(count, 655);
  const draft = { ...forms.plugin.data, 'custom.onebot-11.message_template': 'saved' };
  for (const [key, value] of [
    ['_tabs', '_tab.custom.email'],
    ['custom.bark._config_cipher_dialog_closed', true],
    ['custom.onebot-11.message_template', 'edited'],
  ]) {
    const field = pluginFieldProps({ model: key }, draft);
    field['onUpdate:modelValue'](value);
    assert.equal(draft[key], value);
  }
  const close = pluginFieldProps({ model: 'custom.bark._config_cipher_dialog_closed' }, draft);
  close['onUpdate:modelValue'](false);
  assert.equal(draft['custom.bark._config_cipher_dialog_closed'], false);
  assert.equal(draft['custom.onebot-11.message_template'], 'edited');
  assert.equal(draft.custom, undefined, 'literal dotted keys became a nested config');
  assert.equal(draft['custom.email.from_name'], 'MoviePilot');
});

test('plugin literal model binding cannot access prototypes and links cannot execute scripts', () => {
  for (const key of [
    '__proto__',
    'constructor',
    'prototype',
  ]) {
    assert.notDeepEqual(pluginFormIssues([{ component: 'VTextField', props: { model: key } }]), []);
    assert.equal(Object.hasOwn(pluginFieldProps({ model: key }, {}), 'modelValue'), false);
  }
  const inherited = Object.create({ upstream: 'not-an-own-value' });
  assert.equal(pluginFieldProps({ model: 'upstream' }, inherited).modelValue, undefined);
  for (const href of [
    'javascript:alert(1)',
    'data:text/html,hi',
    '//third.invalid',
    'https://user:password@example.invalid',
    '/local/file',
  ]) {
    assert.equal(validPluginLink(href), false);
    assert.notDeepEqual(pluginFormIssues([{ component: 'a', props: { href } }]), []);
    assert.equal(Object.hasOwn(pluginFieldProps({ href }, {}), 'href'), false);
  }
  assert.equal(validPluginLink('https://github.com/botuniverse/onebot-11'), true);
  assert.deepEqual(
    pluginFormIssues([
      {
        component: 'a',
        props: { href: 'https://github.com/botuniverse/onebot-11', target: '_blank' },
      },
    ]),
    [],
  );
});

test('a newly introduced tab or nested dialog key remains reactive on its first edit', () => {
  for (const key of ['_tabs', 'custom.bark._config_cipher_dialog_closed']) {
    const model = reactive({});
    let rendered;
    const watcher = effect(() => {
      rendered = pluginFieldProps({ model: key }, model).modelValue;
    });
    assert.equal(rendered, undefined);
    const field = pluginFieldProps({ model: key }, model);
    field['onUpdate:modelValue'](true);
    assert.equal(rendered, true);
    field['onUpdate:modelValue'](false);
    assert.equal(rendered, false);
    stop(watcher);
  }
});

test('actual Lark form and source dynamic handlers retain the independent model', () => {
  const original = JSON.parse(
    readFileSync(
      new URL('../../testdata/plugins/lark-messager-0.9.1-form.json', import.meta.url),
      'utf8',
    ),
  );
  assert.deepEqual(pluginFormIssues(original.form), []);
  const fields = [];
  const visit = (nodes) => {
    for (const node of nodes) {
      if (node.props?.model === 'app_secret') fields.push(node);
      if (node.content) visit(node.content);
    }
  };
  visit(original.form);
  assert.equal(fields.length, 1);
  const props = fields[0].props;
  const draft = reactive({ ...original.defaults, app_secret: 'owned-synthetic-value' });
  let rendered;
  const watcher = effect(() => {
    rendered = pluginFieldProps(props, draft);
  });
  // Secret inputs stay plaintext: no password box and no visibility toggle.
  assert.equal(rendered.type, 'text');
  assert.equal(rendered['append-inner-icon'], undefined);
  assert.equal(rendered['onClick:append-inner'], undefined);
  assert.equal(draft.app_secret, 'owned-synthetic-value');
  stop(watcher);
  for (const changes of [
    { 'onClick:append-inner': 'function(e){ fetch("https://foreign.invalid") }' },
    { type: "{{ model.some_other_key ? 'text' : 'password' }}" },
    { model: 'another_password' },
    { onInput: 'function(e){ model.app_secret = e }' },
  ]) {
    assert.deepEqual(pluginFormIssues([{ ...fields[0], props: { ...props, ...changes } }]), []);
  }
});

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
  for (const src of ['/plugin_icon/../../evil.png', 'data:text/html,<script>bad()</script>'])
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
    0,
  );
  assert.equal(
    pluginFormIssues([{ component: 'VSwitch', props: { model: '__proto__' } }]).length,
    1,
  );
  assert.equal(
    pluginFormIssues([{ component: 'VForm', props: { onClick: 'unportedAction()' } }]).length,
    0, // Fixed source accepts any expression; an unknown function fails only when invoked.
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

test('complete original AutoSignIn form retains its cron editor and independent interval draft', () => {
  const form = JSON.parse(
    readFileSync(new URL('../../internal/plugins/autosignin/form.json', import.meta.url), 'utf8'),
  );
  assert.deepEqual(pluginFormIssues(form.conf), []);
  const find = (nodes) => {
    for (const node of nodes) {
      if (node.component === 'VCronField') return node;
      const nested = find(node.content || []);
      if (nested) return nested;
    }
  };
  const node = find(form.conf);
  assert.equal(node.props.model, 'cron');
  const model = { ...form.model, cron: '2.3/9-23', unrelated: false };
  const bound = pluginFieldProps(node.props, model);
  assert.equal(bound.modelValue, '2.3/9-23');
  bound['onUpdate:modelValue']('0 9 * * 5');
  assert.equal(model.cron, '0 9 * * 5');
  bound['onUpdate:modelValue'](null);
  assert.equal(model.cron, null);
  assert.equal(model.unrelated, false);
});

test('complete original IYUU form supports its explanatory span, underline and safe README link', () => {
  const original = JSON.parse(
    readFileSync(new URL('../../internal/plugins/iyuuautoseed/form.json', import.meta.url), 'utf8'),
  );
  assert.deepEqual(pluginFormIssues(original.form), []);
  const tags = [];
  const visit = (nodes) =>
    nodes.forEach((node) => {
      tags.push(node.component);
      if (node.content) visit(node.content);
    });
  visit(original.form);
  assert.equal(tags.length, 51);
  assert.equal(tags.filter((tag) => tag === 'span').length, 1);
  assert.equal(tags.filter((tag) => tag === 'u').length, 1);
  const renderer = readFileSync(
    new URL('../src/components/PluginFormRenderer.vue', import.meta.url),
    'utf8',
  );
  assert.match(renderer, /u: 'u'/);
  assert.match(renderer, /pluginFieldProps\(node.props \|\| \{\}, props.model, node.component\)/);
});

test('source text components retain presentation props and original event expressions', () => {
  const safe = {
    class: 'text-primary',
    title: 'README <safe>',
    style: { color: '#663399', textDecoration: 'underline' },
  };
  for (const component of ['span', 'u']) {
    assert.deepEqual(pluginFieldProps(safe, {}, component), safe);
    assert.deepEqual(
      pluginFormIssues([{ component, props: safe, text: '<script>plain text</script>' }]),
      [],
    );
    for (const key of ['onclick','onClick']) {
      const props = {[key]: '(function(){return e => { model.owned = e }})()'};
      assert.deepEqual(pluginFormIssues([{component,props}]),[]);
      const draft={}; pluginFieldProps(props,draft,component)[key]('value'); assert.equal(draft.owned,'value');
    }
    for (const props of [
      { innerHTML: '<img src=x onerror=alert(1)>' },
    ]) {
      assert.notDeepEqual(pluginFormIssues([{ component, props }]), []);
      assert.deepEqual(pluginFieldProps(props, {}, component), {});
    }
    const presentation = {
      style: { backgroundImage: 'url(https://example.org)' },
      title: 'source title',
    };
    assert.deepEqual(pluginFormIssues([{ component, props: presentation }]), []);
    assert.deepEqual(pluginFieldProps(presentation, {}, component), presentation);
  }
});

test('dynamic link expressions and model aliases validate the resolved URL before binding', () => {
  const model = { project: 'https://owned.invalid/project' };
  for (const href of ['project', '{{model.project}}']) {
    assert.deepEqual(pluginFormIssues([{ component: 'VBtn', props: { href } }]), []);
    assert.equal(pluginFieldProps({ href }, model).href, model.project);
    assert.equal(
      Object.hasOwn(pluginFieldProps({ href }, { project: 'javascript:alert(1)' }), 'href'),
      false,
    );
  }
});

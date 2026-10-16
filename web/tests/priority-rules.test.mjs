import test from 'node:test';
import assert from 'node:assert/strict';
import {
  importRuleCode,
  ruleLevels,
  ruleExpression,
  editRuleIssue,
  groupIssue,
} from '../src/features/priority-rules.ts';
import { builtinRuleOptions } from '../src/features/priority-rule-options.ts';

test('MoviePilot exported custom and group arrays import with only their original schema fields', () => {
  assert.deepEqual(
    importRuleCode(
      '[{"id":"RULE1","name":"合成规则","include":"A|B","secret":"discard"}]',
      'custom',
    ),
    [{ id: 'RULE1', name: '合成规则', include: 'A|B' }],
  );
  assert.deepEqual(
    importRuleCode(
      '[{"name":"规则组1","rule_string":" WEBDL > BLURAY ","media_type":"电影","category":"华语电影","other":"discard"}]',
      'groups',
    ),
    [
      {
        name: '规则组1',
        rule_string: ' WEBDL > BLURAY ',
        media_type: '电影',
        category: '华语电影',
      },
    ],
  );
  for (const code of ['{}', '[null]', '[{"id":"A"}]', '[{"name":"A","id":null}]'])
    assert.throws(() => importRuleCode(code, 'custom'));
  assert.throws(() => importRuleCode('[{"id":"A","name":"A"}]', 'groups'));
  assert.throws(() => importRuleCode('[{"id":"A","name":"A","include":123}]', 'custom'));
});
test('tier import preserves the original spaces, empty tiers and AND tokens when sharing', () => {
  assert.equal(builtinRuleOptions.length, 38);
  assert.equal(builtinRuleOptions.find((item) => item.title === '质量: WEB-DL').value, ' WEBDL ');
  const levels = ruleLevels('WEBDL & 4K > BLURAY', true);
  assert.deepEqual(levels, [[' WEBDL ', ' 4K '], [' BLURAY ']]);
  assert.equal(ruleExpression(levels), ' WEBDL & 4K > BLURAY ');
  assert.deepEqual(ruleLevels(' A >>B '), [[' A '], [], ['B ']]);
  assert.equal(ruleExpression([[], ...levels]), ' WEBDL & 4K > BLURAY ');
});
test('editing rejects duplicate names and IDs while permitting the existing source builtin override quirk', () => {
  assert.ok(editRuleIssue({ id: 'X', name: 'new' }, [{ id: 'X', name: 'other' }]));
  assert.ok(editRuleIssue({ id: 'Y', name: '国语配音' }, []));
  assert.equal(editRuleIssue({ id: 'WEBDL', name: '自定义覆盖' }, []), '');
  assert.ok(groupIssue([{ name: 'same' }, { name: 'same' }]));
});

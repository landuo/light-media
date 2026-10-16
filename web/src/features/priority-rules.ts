import { builtinRuleOptions } from './priority-rule-options.ts';
export type CustomRule = {
  id: string;
  name: string;
  include?: string | null;
  exclude?: string | null;
  size_range?: string | null;
  seeders?: string | null;
  publish_time?: string | null;
};
export type RuleGroup = {
  name: string;
  rule_string?: string | null;
  media_type?: string | null;
  category?: string | null;
};
export type RuleSection = 'custom' | 'groups' | 'priority';
export function customRuleIssue(rules: CustomRule[]): string {
  if (rules.some((rule) => !rule.id || !rule.name)) return '规则ID和规则名称不能为空';
  if (new Set(rules.map((rule) => rule.id)).size !== rules.length) return '规则ID不能重复';
  if (new Set(rules.map((rule) => rule.name)).size !== rules.length) return '规则名称不能重复';
  return '';
}
export function editRuleIssue(rule: CustomRule, others: CustomRule[]): string {
  if (builtinRuleOptions.some((item) => item.value === rule.id))
    return '当前规则ID已被内置规则占用';
  if (builtinRuleOptions.some((item) => item.title === rule.name))
    return '当前规则名称已被内置规则占用';
  return customRuleIssue([...others, rule]);
}
export function groupIssue(groups: RuleGroup[]): string {
  if (groups.some((group) => !group.name.trim())) return '规则组名称不能为空';
  if (new Set(groups.map((group) => group.name)).size !== groups.length) return '规则组名称已存在';
  return '';
}
export function importRuleCode(code: string, section: 'custom'): CustomRule[];
export function importRuleCode(code: string, section: 'groups'): RuleGroup[];
export function importRuleCode(
  code: string,
  section: 'custom' | 'groups',
): (CustomRule | RuleGroup)[] {
  let value: unknown;
  try {
    value = JSON.parse(code);
  } catch {
    throw new Error('导入格式不正确，请使用有效的 JSON 数组');
  }
  if (
    !Array.isArray(value) ||
    value.some(
      (item) =>
        !item ||
        typeof item !== 'object' ||
        !('name' in item) ||
        (section === 'custom' ? !('id' in item) : 'id' in item),
    )
  ) {
    throw new Error('导入格式不正确，请使用对应规则的 JSON 数组');
  }
  const fields =
    section === 'custom'
      ? ['id', 'name', 'include', 'exclude', 'size_range', 'seeders', 'publish_time']
      : ['name', 'rule_string', 'media_type', 'category'];
  return value.map((item) => {
    const rule: Record<string, string | null> = {};
    for (const field of fields) {
      if (!(field in item)) continue;
      if (item[field] !== null && typeof item[field] !== 'string')
        throw new Error('规则字段应为文本');
      rule[field] = item[field];
    }
    if (typeof rule.name !== 'string' || (section === 'custom' && typeof rule.id !== 'string'))
      throw new Error('规则名称或ID格式不正确');
    return rule as CustomRule | RuleGroup;
  });
}
export function ruleLevels(code?: string | null, importing = false): string[][] {
  if (!code) return [];
  if (importing) {
    if (!code.startsWith(' ')) code = ' ' + code;
    if (!code.endsWith(' ')) code += ' ';
  }
  return code.split('>').map((level) => level.split('&').filter(Boolean));
}
export function ruleExpression(levels: string[][]): string {
  return levels
    .filter((level) => level.length)
    .map((level) => level.join('&'))
    .join('>');
}
export function nextRuleName(prefix: string, names: string[]): string {
  let number = names.length + 1;
  while (names.includes(prefix + number)) number++;
  return prefix + number;
}

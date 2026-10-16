export type SubscriptionFilter = 'quality' | 'resolution' | 'effect' | 'include' | 'exclude';
const legacyFields: Record<SubscriptionFilter, string> = {
  quality: 'source',
  resolution: 'resolution',
  effect: 'hdr',
  include: 'include',
  exclude: 'exclude',
};
export function subscriptionFilterValue(
  policy: Record<string, any>,
  field: SubscriptionFilter,
): string {
  const value = policy.filter_params?.[field];
  return value == null ? String(policy[legacyFields[field]] || '') : String(value);
}
export function setSubscriptionFilter(
  policy: Record<string, any>,
  field: SubscriptionFilter,
  value: unknown,
): Record<string, any> {
  return {
    ...policy,
    [legacyFields[field]]: '',
    filter_params: { ...policy.filter_params, [field]: value == null ? '' : String(value) },
  };
}

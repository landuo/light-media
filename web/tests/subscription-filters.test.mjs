import test from 'node:test';
import assert from 'node:assert/strict';
import {
  subscriptionFilterValue,
  setSubscriptionFilter,
} from '../src/features/subscription-filters.ts';
import {
  qualityOptions,
  resolutionOptions,
  effectOptions,
} from '../src/features/subscription-filter-options.ts';
test('source regex fields replace only the edited native field and explicit empty keeps a cleared rule empty', () => {
  const old = {
    source: 'BluRay',
    resolution: '1080p',
    hdr: 'hdr10',
    include: 'OLD',
    codec: 'HEVC',
    filter_params: { effect: 'Atmos' },
  };
  const edited = setSubscriptionFilter(old, 'quality', 'WEB-?DL|WEB-?RIP');
  assert.equal(subscriptionFilterValue(edited, 'quality'), 'WEB-?DL|WEB-?RIP');
  assert.equal(edited.source, '');
  assert.equal(old.source, 'BluRay');
  assert.equal(subscriptionFilterValue(edited, 'effect'), 'Atmos');
  assert.equal(edited.codec, 'HEVC');
  assert.equal(edited.resolution, '1080p');
  const cleared = setSubscriptionFilter(edited, 'effect', null);
  assert.equal(subscriptionFilterValue(cleared, 'effect'), '');
  assert.equal(cleared.hdr, '');
  assert.equal(
    subscriptionFilterValue(
      { resolution: '2160p', filter_params: { resolution: null } },
      'resolution',
    ),
    '2160p',
  );
  assert.equal(
    subscriptionFilterValue(
      { resolution: '2160p', filter_params: { resolution: '' } },
      'resolution',
    ),
    '',
  );
  assert.equal(
    setSubscriptionFilter(old, 'include', String.raw`(?<=S01)E\d+`).filter_params.include,
    String.raw`(?<=S01)E\d+`,
  );
});
test('original resource dropdown options carry regular expressions rather than native enumerations', () => {
  assert.deepEqual(
    [qualityOptions.length, resolutionOptions.length, effectOptions.length],
    [9, 4, 5],
  );
  assert.equal(qualityOptions.find((item) => item.value.includes('WEB')).value, 'WEB-?DL|WEB-?RIP');
  assert.equal(
    resolutionOptions.find((item) => item.value.startsWith('4K')).value,
    '4K|2160p|x2160',
  );
  assert.equal(
    effectOptions.find((item) => item.value.includes('HDR10')).value,
    String.raw`[\s.]+HDR[\s.]+|HDR10|HDR10\+`,
  );
});

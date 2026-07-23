import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const winFormsPlan = new URL(
  '../../docs/superpowers/plans/2026-07-23-aifar-bundle-packager-winforms.md',
  import.meta.url,
);
const conditionalPlan = new URL(
  '../../docs/superpowers/plans/2026-07-24-aifar-bundle-conditional-source-paths.md',
  import.meta.url,
);
const readme = new URL('./README.md', import.meta.url);

const requiredStatusText = [
  '## Implementation Status and Evidence',
  'This section is the authoritative current status.',
  'Historical RED output was not persisted independently',
  'pnpm test:tools',
  '36/36',
];

test('historical implementation plans contain authoritative evidence matrices', async () => {
  const [winForms, conditional] = await Promise.all([
    readFile(winFormsPlan, 'utf8'),
    readFile(conditionalPlan, 'utf8'),
  ]);

  for (const plan of [winForms, conditional]) {
    for (const text of requiredStatusText) {
      assert.match(plan, new RegExp(text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
    }
  }

  for (const task of [1, 2, 3, 4, 5, 6]) {
    assert.match(winForms, new RegExp(`\\| ${task} \\|`));
  }
  assert.match(winForms, /tools\/aifar-bundle-packager\/build\.ps1/);
  assert.doesNotMatch(winForms, /scripts\/build-aifar-bundle-packager\.ps1/);

  for (const task of [1, 2, 3]) {
    assert.match(conditional, new RegExp(`\\| ${task} \\|`));
  }
  for (const commit of ['25414177', '8945d47f', 'a993bd40']) {
    assert.match(conditional, new RegExp(commit));
  }
});

test('README documents the tag, exact assets, and Draft acceptance boundary', async () => {
  const contents = await readFile(readme, 'utf8');

  assert.match(contents, /aifar-bundle-packager-vX\.Y\.Z/);
  assert.match(contents, /AIFARBundlePackager\.exe/);
  assert.match(contents, /AIFARBundlePackager\.exe\.sha256/);
  assert.match(contents, /release-manifest\.json/);
  assert.match(contents, /Draft Release/);
  assert.match(contents, /not stored in Git/i);
});

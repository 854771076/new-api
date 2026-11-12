import fs from 'node:fs';
import path from 'node:path';

const srcDir = 'D:/workplace/code/ai-dev/new-api-monster/web/src';
const files = [
  'features/concurrency/index.tsx',
  'features/concurrency/components/kpi-cards.tsx',
  'features/concurrency/components/scope-table.tsx',
  'features/concurrency/components/degraded-badge.tsx',
];

const keys = new Set();
for (const f of files) {
  const content = fs.readFileSync(path.join(srcDir, f), 'utf8');
  const re = /t\(\s*'([^']+)'/gs;
  let m;
  while ((m = re.exec(content)) !== null) keys.add(m[1]);
}
const realKeys = [...keys].filter(
  (k) => !k.includes('renders') && !k.includes('badge with'),
);

const en = JSON.parse(
  fs.readFileSync(path.join(srcDir, 'i18n/locales/en.json'), 'utf8'),
).translation;

console.log(`Total concurrency keys: ${realKeys.length}\n`);

for (const lang of ['en', 'zh', 'zh-TW', 'fr', 'ru', 'ja', 'vi']) {
  const d = JSON.parse(
    fs.readFileSync(path.join(srcDir, `i18n/locales/${lang}.json`), 'utf8'),
  ).translation;
  const missing = realKeys.filter((k) => !(k in d));
  const untranslated =
    lang === 'en'
      ? []
      : realKeys.filter((k) => k in d && d[k] === en[k]);
  console.log(
    `${lang}: missing=${missing.length} untranslated=${untranslated.length}`,
  );
  for (const k of missing) console.log(`  MISSING: ${k}`);
  if (untranslated.length > 0 && untranslated.length <= 25) {
    for (const k of untranslated) console.log(`  untranslated: ${k}`);
  }
}

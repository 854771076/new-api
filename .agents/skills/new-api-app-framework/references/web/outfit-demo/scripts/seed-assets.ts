#!/usr/bin/env bun
// scripts/seed-assets.ts — 从本地 pack.json 批量入库服装/姿势到 Supabase。
// 用法：在项目根配置好 .env.local 后：
//   cd apps/ai-outfit
//   bun run scripts/seed-assets.ts \
//     --pack-root "C:/Users/Administrator/.../asset-packs" \
//     --client-id "nac_xxxxxxxx"
//
// 幂等：按 (client_id, provider_id) upsert。
// 依赖：bun (已安装)、supabase-js (已安装)。

import { readFileSync, existsSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createClient } from '@supabase/supabase-js';

// ---- 1. 环境准备 ----
const supabaseUrl = process.env.SUPABASE_URL ?? process.env.NEXT_PUBLIC_SUPABASE_URL;
const supabaseSecret = process.env.SUPABASE_SECRET_KEY;
if (!supabaseUrl || !supabaseSecret) {
  console.error('[seed] 需要 SUPABASE_URL + SUPABASE_SECRET_KEY（或 NEXT_PUBLIC_SUPABASE_URL）');
  process.exit(1);
}
const supabase = createClient(supabaseUrl, supabaseSecret, { auth: { autoRefreshToken: false, persistSession: false } });

// ---- 2. CLI 参数 ----
const args = new Map<string, string>();
for (let i = 2; i < process.argv.length; i++) {
  const a = process.argv[i];
  if (a.startsWith('--')) {
    const eq = a.indexOf('=');
    if (eq >= 0) args.set(a.slice(2, eq), a.slice(eq + 1));
    else if (i + 1 < process.argv.length) { args.set(a.slice(2), process.argv[i + 1]); i++; }
  }
}

const packRoot = resolve(args.get('pack-root') ?? '../../../asset-packs');
const clientId = args.get('client-id') ?? '';
if (!clientId) { console.error('[seed] --client-id 必填（OAuth app id）'); process.exit(1); }

const costumePack = join(packRoot, 'costume-library', 'pack.json');
const posePack = join(packRoot, 'pose-library', 'pack.json');
for (const f of [costumePack, posePack]) {
  if (!existsSync(f)) { console.error(`[seed] pack.json 不存在：${f}`); process.exit(1); }
}

console.log('[seed] packRoot =', packRoot);
console.log('[seed] clientId =', clientId);
console.log();

// ---- 3. 工具 ----
function readJson<T = unknown>(p: string): T {
  return JSON.parse(readFileSync(p, 'utf8')) as T;
}

function sleep(ms: number) { return new Promise((r) => setTimeout(r, ms)); }

async function upsertBatch(
  table: string,
  rows: Array<Record<string, unknown>>,
  onConflict: string,
): Promise<number> {
  if (rows.length === 0) return 0;
  const { error } = await supabase
    .from(table)
    .upsert(rows as never, { onConflict, ignoreDuplicates: false });
  if (error) throw error;
  return rows.length;
}

async function uploadWithRetry(bucket: string, path: string, buf: Buffer, contentType: string, retries = 3): Promise<void> {
  let lastErr: unknown;
  for (let attempt = 1; attempt <= retries; attempt++) {
    const { error } = await supabase.storage
      .from(bucket)
      .upload(path, buf, { contentType, upsert: true });
    if (!error) return;
    lastErr = error;
    console.warn(`  upload 第 ${attempt} 次失败：${error.message}`);
    await sleep(500 * attempt);
  }
  throw lastErr;
}

// ---- 4. 服装 ----
async function seedCostumes(): Promise<void> {
  console.log('[seed] 处理服装库…');
  const pack = readJson<{
    media?: { tiers?: Array<{ id: string; dir: string; ext?: string }> };
    items: Array<{
      id: string;
      name: { zh: string; en?: string };
      description?: { zh?: string; en?: string };
      category: string;
      keywords?: string[];
      tags?: string[];
      files: { thumb?: { path: string }; preview?: { path: string } };
    }>;
  }>(costumePack);

  const imgRoot = join(packRoot, 'costume-library');
  const bucket = 'media-official';
  const BATCH = 100;

  // 从 pack.media.tiers 检测目录存在性，做缺失 fallback
  const tierDirs = new Map<string, string>(); // tierId -> 实际存在的 dir
  if (pack.media?.tiers) {
    for (const t of pack.media.tiers) {
      const abs = join(imgRoot, t.dir);
      tierDirs.set(t.id, existsSync(abs) ? t.dir : null as unknown as string);
      console.log(`  tier ${t.id}: dir="${t.dir}" → ${existsSync(abs) ? '存在' : '缺失'}`);
    }
  }
  // 如果 thumb tier 缺失，用 preview tier 的顶上
  const thumbDirOk = tierDirs.get('thumb');
  const previewDirOk = tierDirs.get('preview');
  const usePreviewAsThumb = !thumbDirOk && !!previewDirOk;
  if (usePreviewAsThumb) console.log('  → thumb 目录缺失，preview 图兼做 thumb');

  // category 可以从文件名前缀取（AUT/SUM/SPR/SWM/LNG/PTY...），
  // 但 pack.json 里每个 item 已有 category 字段，直接用

  let ok = 0;
  let err = 0;

  for (let i = 0; i < pack.items.length; i += BATCH) {
    const slice = pack.items.slice(i, i + BATCH);
    const records: Array<Record<string, unknown>> = [];
    for (const item of slice) {
      const id = item.id;
      const previewPath = item.files.preview?.path;
      let thumbPath = item.files.thumb?.path;

      // thumb 缺失 → fallback 到 preview（目录 + 文件名完全一样）
      if ((!thumbPath || !existsSync(join(imgRoot, thumbPath))) && previewPath) {
        thumbPath = previewPath;
      }
      if (!thumbPath || !previewPath) { err++; continue; }

      const thumbAbs = join(imgRoot, thumbPath);
      const previewAbs = join(imgRoot, previewPath);
      if (!existsSync(thumbAbs) || !existsSync(previewAbs)) { err++; continue; }

      // 上传 thumb（可能和 preview 是同一个文件，但用不同路径 + 不同文件名存储）
      const thumbBuf = readFileSync(thumbAbs);
      const thumbExt = thumbAbs.split('.').pop() ?? 'webp';
      const thumbDest = `${clientId}/outfits-thumbs/${id}.${thumbExt}`;
      try {
        await uploadWithRetry(bucket, thumbDest, thumbBuf, `image/${thumbExt === 'jpg' ? 'jpeg' : thumbExt}`);
      } catch (e) { console.warn(`  thumb 上传失败 ${id}:`, e); err++; continue; }

      // 上传 preview
      const previewBuf = readFileSync(previewAbs);
      const previewExt = previewAbs.split('.').pop() ?? 'webp';
      const previewDest = `${clientId}/outfits/${id}.${previewExt}`;
      try {
        await uploadWithRetry(bucket, previewDest, previewBuf, `image/${previewExt === 'jpg' ? 'jpeg' : previewExt}`);
      } catch (e) { console.warn(`  preview 上传失败 ${id}:`, e); err++; continue; }

      records.push({
        client_id: clientId,
        provider_id: id,
        category: item.category,
        tags: [], // pack.json tags 是 object，不直接映射
        name_zh: item.name.zh,
        name_en: item.name.en ?? null,
        description_zh: item.description?.zh ?? null,
        description_en: item.description?.en ?? null,
        keywords: item.keywords ?? [],
        bucket,
        thumb_path: thumbDest,
        preview_path: previewDest,
      });
    }

    if (records.length > 0) {
      const written = await upsertBatch('outfits', records, 'client_id,provider_id');
      ok += written;
      console.log(`  [${i + 1}-${i + slice.length}] upsert 成功 ${written} 条，累计 ok=${ok} err=${err}`);
    }
  }
  console.log(`[seed] 服装完成：ok=${ok} err=${err}`);
}

// ---- 5. 姿势 ----
async function seedPoses(): Promise<void> {
  console.log('[seed] 处理姿势库…');
  const pack = readJson<{
    items: Array<{
      id: string;
      name: { zh: string; en?: string };
      description?: { zh?: string; en?: string };
      category: string;
      tags?: { system?: string; category?: string; angle?: string };
      files: { thumb?: { path: string }; preview?: { path: string } };
    }>;
  }>(posePack);

  const imgRoot = join(packRoot, 'pose-library');
  const bucket = 'media-official';
  const BATCH = 100;

  let ok = 0;
  let err = 0;

  for (let i = 0; i < pack.items.length; i += BATCH) {
    const slice = pack.items.slice(i, i + BATCH);
    const records: Array<Record<string, unknown>> = [];
    for (const item of slice) {
      const id = item.id;
      const thumbPath = item.files.thumb?.path;
      const previewPath = item.files.preview?.path;
      if (!thumbPath || !previewPath) { err++; continue; }

      const thumbAbs = join(imgRoot, thumbPath);
      const previewAbs = join(imgRoot, previewPath);
      if (!existsSync(thumbAbs) || !existsSync(previewAbs)) { err++; continue; }

      const thumbBuf = readFileSync(thumbAbs);
      const thumbExt = thumbAbs.split('.').pop() ?? 'webp';
      const thumbDest = `${clientId}/poses-thumbs/${id}.${thumbExt}`;
      try {
        await uploadWithRetry(bucket, thumbDest, thumbBuf, `image/${thumbExt === 'jpg' ? 'jpeg' : thumbExt}`);
      } catch (e) { err++; continue; }

      const previewBuf = readFileSync(previewAbs);
      const previewExt = previewAbs.split('.').pop() ?? 'webp';
      const previewDest = `${clientId}/poses/${id}.${previewExt}`;
      try {
        await uploadWithRetry(bucket, previewDest, previewBuf, `image/${previewExt === 'jpg' ? 'jpeg' : previewExt}`);
      } catch (e) { err++; continue; }

      records.push({
        client_id: clientId,
        provider_id: id,
        pose_set: id.split('-').slice(0, -1).join('-'), // 去掉 -front/-side 角度后缀
        system: item.tags?.system ?? '未分类',
        category: item.category,
        angle: item.tags?.angle ?? '正面',
        name_zh: item.name.zh,
        name_en: item.name.en ?? null,
        description_zh: item.description?.zh ?? null,
        description_en: item.description?.en ?? null,
        bucket,
        thumb_path: thumbDest,
        preview_path: previewDest,
      });
    }

    if (records.length > 0) {
      const written = await upsertBatch('poses', records, 'client_id,provider_id');
      ok += written;
      console.log(`  [${i + 1}-${i + slice.length}] upsert 成功 ${written} 条，累计 ok=${ok} err=${err}`);
    }
  }
  console.log(`[seed] 姿势完成：ok=${ok} err=${err}`);
}

// ---- 6. run ----
(async () => {
  try {
    // 验证源文件可达
    const stat = statSync(packRoot);
    if (!stat.isDirectory()) { console.error('[seed] --pack-root 必须是目录'); process.exit(1); }

    await seedCostumes();
    console.log();
    await seedPoses();
    console.log();
    console.log('[seed] 全部完成 ✅');
  } catch (e) {
    console.error('[seed] 失败：', e);
    process.exit(1);
  }
})();

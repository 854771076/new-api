// lib/tryon/manifest.ts — 与服装板格位同序的结构化清单（cell 从 1 开始）。
import 'server-only';
import type { GarmentManifestEntry, GarmentSemanticHints, GarmentSource } from '@/lib/types';

export interface ManifestInput {
  id: string;
  source: GarmentSource;
  category: string;
  name: string;
  garment_type?: string | null;
  semantic_hints?: GarmentSemanticHints | null;
}

/**
 * 按稳定 ID 排序后生成格位清单，顺序必须与 buildGarmentBoard 的 orderedIds 完全一致。
 * 返回紧凑 JSON（无空白），供 v4 提示词的 {garment_manifest} 占位符使用。
 */
export function buildGarmentManifest(items: ManifestInput[]): { json: string; entries: GarmentManifestEntry[] } {
  const ordered = [...items].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  const entries: GarmentManifestEntry[] = ordered.map((item, index) => ({
    cell: index + 1,
    source: item.source,
    category: item.category,
    name: item.name,
    garment_type: item.garment_type ?? null,
    semantic_hints: item.semantic_hints ?? {},
  }));
  return { json: JSON.stringify(entries), entries };
}

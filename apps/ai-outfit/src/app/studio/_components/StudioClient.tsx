'use client';

import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { useState, useCallback, Suspense } from 'react';
import { MediaCard } from '@/components/MediaCard';
import { CharacterUploader } from '@/components/CharacterUploader';
import { Sparkles, Upload, Search, X, ChevronRight } from 'lucide-react';

type Item = { id: string; name: string; subtitle?: string; signedUrl?: string | null };

type ModelOption = {
  id: string;
  model_id: string;
  display_name: string;
  provider?: string | null;
  is_default: boolean;
};

type Props = {
  characters: Item[];
  outfits: Item[];
  poses: Item[];
  outfitCats: string[];
  poseSystems: string[];
  poseAngles: string[];
  initialOutfitQ: string;
  initialOutfitCat: string;
  initialPoseQ: string;
  initialPoseSys: string;
  initialPoseCat: string;
  initialPoseAngle: string;
  models: ModelOption[];
};

function Chip({
  active, onClick, children,
}: { active?: boolean; onClick?: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      className={`btn btn-xs normal-case ${
        active ? 'btn-primary' : 'btn-ghost bg-base-200 text-base-content/70 hover:bg-base-300'
      }`}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

function StudioClientInner({
  characters, outfits, poses,
  outfitCats, poseSystems, poseAngles,
  initialOutfitQ, initialOutfitCat,
  initialPoseQ, initialPoseSys, initialPoseCat, initialPoseAngle,
  models,
}: Props) {
  const router = useRouter();
  const searchParams = useSearchParams();

  const [charId, setCharId] = useState<string | null>(null);
  const [outfitIds, setOutfitIds] = useState<string[]>([]);
  const [poseId, setPoseId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [modelId, setModelId] = useState(
    models.find((m) => m.is_default)?.model_id ?? models[0]?.model_id ?? 'gpt-image-1',
  );
  const [promptExtra, setPromptExtra] = useState('');

  // 搜索/筛选本地态（提交后跳转到带 query 的 URL）
  const [outfitQ, setOutfitQ] = useState(initialOutfitQ);
  const [outfitCat, setOutfitCat] = useState(initialOutfitCat);
  const [poseQ, setPoseQ] = useState(initialPoseQ);
  const [poseSys, setPoseSys] = useState(initialPoseSys);
  const [poseCat, setPoseCat] = useState(initialPoseCat);
  const [poseAngle, setPoseAngle] = useState(initialPoseAngle);

  const [charUploadOpen, setCharUploadOpen] = useState(false);

  // 保留其他 URL 参数的同时更新 studio 筛选
  const buildUrl = useCallback((patch: Record<string, string>) => {
    const params = new URLSearchParams(searchParams.toString());
    for (const [k, v] of Object.entries(patch)) {
      if (v) params.set(k, v); else params.delete(k);
    }
    const qs = params.toString();
    return qs ? `/studio?${qs}` : '/studio';
  }, [searchParams]);

  const commitFilters = useCallback(() => {
    router.push(buildUrl({
      outfit_q: outfitQ, outfit_cat: outfitCat,
      pose_q: poseQ, pose_sys: poseSys, pose_cat: poseCat, pose_angle: poseAngle,
    }));
  }, [router, buildUrl, outfitQ, outfitCat, poseQ, poseSys, poseCat, poseAngle]);

  const toggleOutfit = useCallback(
    (id: string) => {
      setOutfitIds((prev) =>
        prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
      );
    },
    [],
  );

  const canGenerate = Boolean(charId && outfitIds.length > 0 && poseId);

  async function handleGenerate() {
    if (!canGenerate || loading) return;
    setLoading(true);
    setErr(null);
    try {
      const res = await fetch('/api/generate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          character_id: charId,
          outfit_ids: outfitIds,
          pose_id: poseId,
          model: modelId,
          prompt_extra: promptExtra.trim() || undefined,
        }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data?.error?.message ?? data?.error ?? '生成失败');
      router.push('/history');
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  const clearAllFilters = () => {
    setOutfitQ(''); setOutfitCat('');
    setPoseQ(''); setPoseSys(''); setPoseCat(''); setPoseAngle('');
    router.push('/studio');
  };

  return (
    <div className="space-y-4">
      {/* 顶部 header */}
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold">换装工作室</h1>
          <p className="mt-1 text-sm text-base-content/60">依次选择人物、服装和姿势，然后生成换装效果。</p>
        </div>
        <div className="flex items-center gap-2 text-xs text-base-content/60">
          {[
            charId ? `1 人物` : null,
            outfitIds.length > 0 ? `${outfitIds.length} 服装` : null,
            poseId ? `1 姿势` : null,
          ].filter(Boolean).join(' · ') || '未选择素材'}
          {(outfitQ || outfitCat || poseQ || poseSys || poseCat || poseAngle) ? (
            <button type="button" className="btn btn-ghost btn-xs gap-1" onClick={clearAllFilters}>
              <X size={12} />清除筛选
            </button>
          ) : null}
        </div>
      </div>

      <div className="grid items-start gap-3 md:grid-cols-[200px_minmax(0,1fr)_minmax(0,1fr)]">
        {/* 左栏：人物 */}
        <section className="card border border-base-300 bg-base-100 shadow-sm">
          <div className="card-body">
            <h2 className="card-title text-base">
              <span className="badge badge-primary badge-sm">1</span>
              选择人物
              <span className="ml-auto text-xs font-normal text-base-content/50">{characters.length} 个</span>
            </h2>

            {/* 搜索 + chips（人物暂无 category 筛选） */}
            <div className="flex flex-wrap items-center gap-2">
              <button type="button" className="btn btn-xs btn-primary gap-1" onClick={() => setCharUploadOpen(true)}>
                <Upload size={12} />上传新人物
              </button>
              <Link href="/characters" className="btn btn-xs btn-ghost gap-1">
                去人物库 <ChevronRight size={12} />
              </Link>
            </div>

            {characters.length > 0 ? (
              <>
                <div className="grid max-h-56 grid-cols-4 gap-1.5 overflow-y-auto pr-1 sm:grid-cols-5 md:grid-cols-2">
                  {characters.map((c) => (
                    <MediaCard
                      key={c.id}
                      title={c.name}
                      signedUrl={c.signedUrl}
                      variant="tile"
                      selected={charId === c.id}
                      onSelect={() => setCharId(c.id === charId ? null : c.id)}
                    />
                  ))}
                </div>
                <p className="text-xs text-base-content/50">已选 {charId ? 1 : 0} / {characters.length}</p>
              </>
            ) : (
              <div className="rounded-box border border-dashed border-base-300 bg-base-200/40 p-6 text-center">
                <p className="text-sm text-base-content/60">还没有上传过人物</p>
                <button
                  type="button"
                  className="btn btn-primary btn-sm mt-3 gap-1"
                  onClick={() => setCharUploadOpen(true)}
                >
                  <Upload size={14} />上传人物
                </button>
              </div>
            )}
          </div>
        </section>

        {/* 中栏：服装 */}
        <section className="card border border-base-300 bg-base-100 shadow-sm">
          <div className="card-body">
            <h2 className="card-title text-base">
              <span className="badge badge-primary badge-sm">2</span>
              选择服装
              <span className="ml-auto text-xs font-normal text-base-content/50">已选 {outfitIds.length} / {outfits.length}</span>
            </h2>

            {/* 搜索输入框 */}
            <div className="flex w-full gap-2">
              <label className="input input-sm flex flex-1 items-center gap-2">
                <Search size={14} className="text-base-content/50" />
                <input
                  type="text"
                  className="grow"
                  placeholder="搜索服装名称/描述"
                  value={outfitQ}
                  onChange={(e) => setOutfitQ(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter') commitFilters(); }}
                />
              </label>
              <button type="button" className="btn btn-sm btn-primary" onClick={commitFilters}>搜索</button>
            </div>

            {/* category chips */}
            {outfitCats.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                <Chip active={!outfitCat} onClick={() => { setOutfitCat(''); commitFilters(); }}>全部</Chip>
                {outfitCats.map((c) => (
                  <Chip key={c} active={outfitCat === c} onClick={() => { setOutfitCat(c); commitFilters(); }}>{c}</Chip>
                ))}
                <Link href="/wardrobe" className="btn btn-xs btn-ghost gap-1">更多 <ChevronRight size={12} /></Link>
              </div>
            ) : null}

            {outfits.length > 0 ? (
              <>
                <div className="grid max-h-[22rem] grid-cols-4 gap-1.5 overflow-y-auto pr-1 sm:grid-cols-5 md:grid-cols-3 xl:grid-cols-4">
                  {outfits.map((o) => (
                    <div key={o.id} className="relative">
                      <MediaCard
                        title={o.name}
                        subtitle={o.subtitle}
                        signedUrl={o.signedUrl}
                        variant="tile"
                        selected={outfitIds.includes(o.id)}
                        onSelect={() => toggleOutfit(o.id)}
                      />
                      {outfitIds.includes(o.id) ? (
                        <span className="absolute right-1 top-1 badge badge-primary badge-xs">✓</span>
                      ) : null}
                    </div>
                  ))}
                </div>
              </>
            ) : (
              <div className="rounded-box border border-dashed border-base-300 bg-base-200/40 p-6 text-center">
                <p className="text-sm text-base-content/60">服装库暂无匹配数据</p>
                <Link href="/wardrobe" className="btn btn-primary btn-sm mt-3">去服装库</Link>
              </div>
            )}
          </div>
        </section>

        {/* 右栏：姿势 + 生成 */}
        <section className="card border border-base-300 bg-base-100 shadow-sm">
          <div className="card-body">
            <h2 className="card-title text-base">
              <span className="badge badge-primary badge-sm">3</span>
              选择姿势
              <span className="ml-auto text-xs font-normal text-base-content/50">{poses.length} 个</span>
            </h2>

            {/* 搜索 + 三组 chips */}
            <div className="flex w-full gap-2">
              <label className="input input-sm flex flex-1 items-center gap-2">
                <Search size={14} className="text-base-content/50" />
                <input
                  type="text"
                  className="grow"
                  placeholder="搜索姿势"
                  value={poseQ}
                  onChange={(e) => setPoseQ(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter') commitFilters(); }}
                />
              </label>
              <button type="button" className="btn btn-sm btn-primary" onClick={commitFilters}>搜索</button>
            </div>

            {/* system chips */}
            {poseSystems.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                <Chip active={!poseSys} onClick={() => { setPoseSys(''); commitFilters(); }}>全部系统</Chip>
                {poseSystems.slice(0, 6).map((s) => (
                  <Chip key={s} active={poseSys === s} onClick={() => { setPoseSys(s); commitFilters(); }}>{s}</Chip>
                ))}
              </div>
            ) : null}
            {poseAngles.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                <Chip active={!poseAngle} onClick={() => { setPoseAngle(''); commitFilters(); }}>全部角度</Chip>
                {poseAngles.slice(0, 8).map((a) => (
                  <Chip key={a} active={poseAngle === a} onClick={() => { setPoseAngle(a); commitFilters(); }}>{a}</Chip>
                ))}
                <Link href="/poses" className="btn btn-xs btn-ghost gap-1">更多 <ChevronRight size={12} /></Link>
              </div>
            ) : null}

            {poses.length > 0 ? (
              <div className="grid max-h-[18rem] grid-cols-5 gap-1.5 overflow-y-auto pr-1 sm:grid-cols-6 md:grid-cols-3 xl:grid-cols-4">
                {poses.map((p) => (
                  <div key={p.id} className="relative">
                    <MediaCard
                      title={p.name}
                      subtitle={p.subtitle}
                      signedUrl={p.signedUrl}
                      variant="tile"
                      selected={poseId === p.id}
                      onSelect={() => setPoseId(p.id === poseId ? null : p.id)}
                    />
                    {poseId === p.id ? (
                      <span className="absolute right-1 top-1 badge badge-primary badge-xs">✓</span>
                    ) : null}
                  </div>
                ))}
              </div>
            ) : (
              <div className="rounded-box border border-dashed border-base-300 bg-base-200/40 p-6 text-center">
                <p className="text-sm text-base-content/60">姿势库暂无匹配数据</p>
                <Link href="/poses" className="btn btn-primary btn-sm mt-3">去姿势库</Link>
              </div>
            )}

            {/* 生成选项 */}
            <section className="card border border-base-300 bg-base-100 shadow-sm">
              <div className="card-body">
                <h2 className="card-title text-base">
                  <span className="badge badge-primary badge-sm">4</span>
                  生成选项
                </h2>

                <label className="flex w-full flex-col gap-1">
                  <span className="text-xs text-base-content/70">模型</span>
                  <select
                    className="select select-sm w-full"
                    value={modelId}
                    onChange={(e) => setModelId(e.target.value)}
                  >
                    {models.map((m) => (
                      <option key={m.id} value={m.model_id}>
                        {m.provider ? `${m.display_name}（${m.provider}）` : m.display_name}
                      </option>
                    ))}
                  </select>
                </label>

                <label className="flex w-full flex-col gap-1">
                  <span className="text-xs text-base-content/70">补充提示词</span>
                  <textarea
                    className="textarea textarea-sm w-full"
                    rows={3}
                    maxLength={500}
                    placeholder="可选：补充风格、光线、背景等要求"
                    value={promptExtra}
                    onChange={(e) => setPromptExtra(e.target.value)}
                  />
                  <span className="self-end text-xs text-base-content/50">{promptExtra.length}/500</span>
                </label>
              </div>
            </section>

            {/* 生成按钮 */}
            <div className="mt-4 border-t border-base-300 pt-4">
              <button
                type="button"
                className="btn btn-primary btn-block gap-2"
                disabled={!canGenerate || loading}
                onClick={handleGenerate}
              >
                {loading ? <span className="loading loading-spinner" /> : <Sparkles size={16} />}
                {loading ? '生成中…' : '开始生成'}
              </button>
              {err ? (
                <div role="alert" className="alert alert-error alert-soft mt-2 text-sm">
                  <span className="flex-1">{err}</span>
                  <button type="button" className="btn btn-sm btn-ghost" onClick={handleGenerate}>
                    重试
                  </button>
                </div>
              ) : null}
              {!canGenerate && !loading ? (
                <p className="mt-2 text-center text-xs text-base-content/50">
                  需要选择 1 个人物 + 至少 1 套服装 + 1 个姿势
                </p>
              ) : null}
            </div>
          </div>
        </section>
      </div>

      {/* 人物上传 Modal */}
      {charUploadOpen ? (
        <div className="modal modal-open" role="dialog" aria-modal="true" aria-label="上传人物">
          <div className="modal-box max-w-lg">
            <h3 className="mb-3 text-lg font-bold">上传新人物</h3>
            <CharacterUploader
              onClose={() => setCharUploadOpen(false)}
              onUploaded={() => { setCharUploadOpen(false); router.refresh(); }}
            />
          </div>
          <button
            type="button"
            className="modal-backdrop"
            aria-label="关闭"
            onClick={() => setCharUploadOpen(false)}
          />
        </div>
      ) : null}
    </div>
  );
}

// Suspense 包装：useSearchParams 在静态边界下需要
export function StudioClient(props: Props) {
  return (
    <Suspense fallback={<div className="alert alert-info alert-soft">加载中…</div>}>
      <StudioClientInner {...props} />
    </Suspense>
  );
}

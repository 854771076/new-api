'use client';

import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { useState, useCallback, useEffect, useRef, Suspense } from 'react';
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
      aria-pressed={Boolean(active)}
      className={`btn btn-sm min-h-11 max-w-full whitespace-normal normal-case ${
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
    models.find((m) => m.is_default)?.model_id ?? models[0]?.model_id ?? '',
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
  const uploadDialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    if (charUploadOpen) uploadDialog.current?.showModal();
    else uploadDialog.current?.close();
  }, [charUploadOpen]);

  // 保留其他 URL 参数的同时更新 studio 筛选
  const buildUrl = useCallback((patch: Record<string, string>) => {
    const params = new URLSearchParams(searchParams.toString());
    for (const [k, v] of Object.entries(patch)) {
      if (v) params.set(k, v); else params.delete(k);
    }
    const qs = params.toString();
    return qs ? `/studio?${qs}` : '/studio';
  }, [searchParams]);

  const commitFilters = useCallback((patch: Record<string, string> = {}) => {
    router.push(buildUrl({
      outfit_q: outfitQ, outfit_cat: outfitCat,
      pose_q: poseQ, pose_sys: poseSys, pose_cat: poseCat, pose_angle: poseAngle,
      ...patch,
    }));
  }, [router, buildUrl, outfitQ, outfitCat, poseQ, poseSys, poseCat, poseAngle]);

  const toggleOutfit = useCallback(
    (id: string) => {
      setOutfitIds((prev) => {
        if (prev.includes(id)) return prev.filter((x) => x !== id);
        if (prev.length >= 6) return prev;
        return [...prev, id];
      });
    },
    [],
  );

  const canGenerate = Boolean(charId && outfitIds.length > 0 && poseId && models.some((m) => m.model_id === modelId));

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
      const data: { success?: boolean; message?: string } = await res.json();
      if (res.status === 401) {
        router.replace('/login?error=session_expired');
        return;
      }
      if (!res.ok || !data.success) throw new Error(data.message ?? '生成失败，请稍后重试');
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
    <div className="space-y-8">
      <header className="space-y-5">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div className="space-y-2">
            <span className="badge badge-outline gap-2"><Sparkles size={14} aria-hidden="true" /> AI 换装</span>
            <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">换装工作室</h1>
            <p className="text-base leading-relaxed text-base-content/70">依次选择人物、服装和姿势，然后生成换装效果。</p>
          </div>
          {(outfitQ || outfitCat || poseQ || poseSys || poseCat || poseAngle) ? (
            <button type="button" className="btn btn-ghost min-h-11 gap-2" onClick={clearAllFilters}>
              <X size={16} aria-hidden="true" />清除筛选
            </button>
          ) : null}
        </div>
        <ul className="steps w-full text-sm" aria-label="素材选择进度">
          <li className={`step ${charId ? 'step-primary' : ''}`}><a href="#studio-character" className="link link-hover">选择人物</a></li>
          <li className={`step ${outfitIds.length > 0 ? 'step-primary' : ''}`}><a href="#studio-outfit" className="link link-hover">选择服装</a></li>
          <li className={`step ${poseId ? 'step-primary' : ''}`}><a href="#studio-pose" className="link link-hover">选择姿势</a></li>
        </ul>
      </header>

      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem] xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0 space-y-6">
          <section id="studio-character" aria-labelledby="studio-character-title" className="card card-border scroll-mt-24 bg-base-100 shadow-sm">
            <div className="card-body gap-5 p-4 sm:p-6">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 id="studio-character-title" className="card-title text-xl"><span className="badge badge-primary">1</span>选择人物</h2>
                <span className="badge badge-outline">{characters.length} 个</span>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <button type="button" className="btn btn-outline min-h-11 gap-2" onClick={() => setCharUploadOpen(true)}>
                  <Upload size={16} aria-hidden="true" />上传新人物
                </button>
                <Link href="/characters" className="btn btn-ghost min-h-11 gap-1">去人物库 <ChevronRight size={16} aria-hidden="true" /></Link>
              </div>
              {characters.length > 0 ? (
                <div className="grid max-h-[32rem] grid-cols-2 gap-4 overflow-y-auto p-1 sm:grid-cols-3 xl:grid-cols-4">
                  {characters.map((c) => (
                    <MediaCard key={c.id} title={c.name} signedUrl={c.signedUrl} variant="tile"
                      selected={charId === c.id} onSelect={() => setCharId(c.id === charId ? null : c.id)} />
                  ))}
                </div>
              ) : (
                <div className="card card-border border-dashed bg-base-200 text-base-content">
                  <div className="card-body items-center gap-3 py-8 text-center">
                    <p className="text-base-content/70">还没有上传过人物</p>
                    <button type="button" className="btn btn-outline gap-2" onClick={() => setCharUploadOpen(true)}><Upload size={16} aria-hidden="true" />上传人物</button>
                  </div>
                </div>
              )}
            </div>
          </section>

          <section id="studio-outfit" aria-labelledby="studio-outfit-title" className="card card-border scroll-mt-24 bg-base-100 shadow-sm">
            <div className="card-body gap-5 p-4 sm:p-6">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 id="studio-outfit-title" className="card-title text-xl"><span className="badge badge-primary">2</span>选择服装</h2>
                <span className="badge badge-outline tabular-nums" aria-live="polite">已选 {outfitIds.length} / 6 套</span>
              </div>
              <fieldset className="fieldset min-w-0 gap-2 pt-0">
                <legend className="fieldset-legend pb-2 text-sm"><label htmlFor="studio-outfit-search">搜索服装</label></legend>
                <div className="flex w-full gap-2">
                  <label className="input min-w-0 flex-1 gap-2">
                    <Search size={18} className="shrink-0 text-base-content/70" aria-hidden="true" />
                    <input id="studio-outfit-search" type="search" className="min-w-0 grow text-base" aria-label="搜索服装名称/描述" placeholder="搜索服装名称/描述"
                      value={outfitQ} onChange={(e) => setOutfitQ(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') commitFilters(); }} />
                  </label>
                  <button type="button" className="btn btn-outline" onClick={() => commitFilters()}>搜索</button>
                </div>
              </fieldset>
              {outfitCats.length > 0 ? (
                <div className="flex flex-wrap gap-2" aria-label="服装分类">
                  <Chip active={!outfitCat} onClick={() => { setOutfitCat(''); commitFilters({ outfit_cat: '' }); }}>全部</Chip>
                  {outfitCats.map((c) => (
                    <Chip key={c} active={outfitCat === c} onClick={() => { setOutfitCat(c); commitFilters({ outfit_cat: c }); }}>{c}</Chip>
                  ))}
                  <Link href="/wardrobe" className="btn btn-sm btn-ghost min-h-11 gap-1">更多 <ChevronRight size={16} aria-hidden="true" /></Link>
                </div>
              ) : null}
              {outfits.length > 0 ? (
                <div className="grid max-h-[36rem] grid-cols-2 gap-4 overflow-y-auto p-1 sm:grid-cols-3 xl:grid-cols-4">
                  {outfits.map((o) => (
                    <MediaCard key={o.id} title={o.name} subtitle={o.subtitle} signedUrl={o.signedUrl} variant="tile"
                      selected={outfitIds.includes(o.id)} disabled={outfitIds.length >= 6 && !outfitIds.includes(o.id)} onSelect={() => toggleOutfit(o.id)} />
                  ))}
                </div>
              ) : (
                <div className="card card-border border-dashed bg-base-200 text-base-content">
                  <div className="card-body items-center gap-3 py-8 text-center">
                    <p className="text-base-content/70">服装库暂无匹配数据</p>
                    <Link href="/wardrobe" className="btn btn-outline">去服装库</Link>
                  </div>
                </div>
              )}
              {outfitIds.length >= 6 ? <p role="status" className="text-sm text-base-content/70">最多选择 6 套服装，取消一套后可继续选择。</p> : null}
            </div>
          </section>

          <section id="studio-pose" aria-labelledby="studio-pose-title" className="card card-border scroll-mt-24 bg-base-100 shadow-sm">
            <div className="card-body gap-5 p-4 sm:p-6">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 id="studio-pose-title" className="card-title text-xl"><span className="badge badge-primary">3</span>选择姿势</h2>
                <span className="badge badge-outline">{poses.length} 个</span>
              </div>
              <fieldset className="fieldset min-w-0 gap-2 pt-0">
                <legend className="fieldset-legend pb-2 text-sm"><label htmlFor="studio-pose-search">搜索姿势</label></legend>
                <div className="flex w-full gap-2">
                  <label className="input min-w-0 flex-1 gap-2">
                    <Search size={18} className="shrink-0 text-base-content/70" aria-hidden="true" />
                    <input id="studio-pose-search" type="search" className="min-w-0 grow text-base" aria-label="搜索姿势" placeholder="搜索姿势" value={poseQ}
                      onChange={(e) => setPoseQ(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') commitFilters(); }} />
                  </label>
                  <button type="button" className="btn btn-outline" onClick={() => commitFilters()}>搜索</button>
                </div>
              </fieldset>
              {poseSystems.length > 0 ? (
                <div className="flex flex-wrap gap-2" aria-label="姿势系统">
                  <Chip active={!poseSys} onClick={() => { setPoseSys(''); commitFilters({ pose_sys: '' }); }}>全部系统</Chip>
                  {poseSystems.slice(0, 6).map((s) => (
                    <Chip key={s} active={poseSys === s} onClick={() => { setPoseSys(s); commitFilters({ pose_sys: s }); }}>{s}</Chip>
                  ))}
                </div>
              ) : null}
              {poseAngles.length > 0 ? (
                <div className="flex flex-wrap gap-2" aria-label="姿势角度">
                  <Chip active={!poseAngle} onClick={() => { setPoseAngle(''); commitFilters({ pose_angle: '' }); }}>全部角度</Chip>
                  {poseAngles.slice(0, 8).map((a) => (
                    <Chip key={a} active={poseAngle === a} onClick={() => { setPoseAngle(a); commitFilters({ pose_angle: a }); }}>{a}</Chip>
                  ))}
                  <Link href="/poses" className="btn btn-sm btn-ghost min-h-11 gap-1">更多 <ChevronRight size={16} aria-hidden="true" /></Link>
                </div>
              ) : null}
              {poses.length > 0 ? (
                <div className="grid max-h-[36rem] grid-cols-2 gap-4 overflow-y-auto p-1 sm:grid-cols-3 xl:grid-cols-4">
                  {poses.map((p) => (
                    <MediaCard key={p.id} title={p.name} subtitle={p.subtitle} signedUrl={p.signedUrl} variant="tile"
                      selected={poseId === p.id} onSelect={() => setPoseId(p.id === poseId ? null : p.id)} />
                  ))}
                </div>
              ) : (
                <div className="card card-border border-dashed bg-base-200 text-base-content">
                  <div className="card-body items-center gap-3 py-8 text-center">
                    <p className="text-base-content/70">姿势库暂无匹配数据</p>
                    <Link href="/poses" className="btn btn-outline">去姿势库</Link>
                  </div>
                </div>
              )}
            </div>
          </section>
        </div>

        <aside aria-labelledby="studio-summary-title" className="card card-border min-w-0 bg-base-100 shadow-md lg:sticky lg:top-24 lg:max-h-[calc(100dvh-7rem)] lg:overflow-y-auto">
          <div className="card-body gap-5 p-5 sm:p-6">
            <div className="space-y-2">
              <span className="badge badge-primary badge-soft">创作预览</span>
              <h2 id="studio-summary-title" className="card-title text-xl">生成选项</h2>
              <p className="text-sm text-base-content/70">确认素材与模型，生成你的新造型。</p>
            </div>
            <dl className="space-y-3 text-sm" aria-live="polite">
              <div className="flex items-start justify-between gap-4"><dt className="shrink-0 text-base-content/70">人物</dt><dd className="text-right font-medium wrap-anywhere">{characters.find((c) => c.id === charId)?.name || '未选择'}</dd></div>
              <div className="flex items-start justify-between gap-4"><dt className="shrink-0 text-base-content/70">服装</dt><dd className="text-right font-medium">{outfitIds.length > 0 ? `已选 ${outfitIds.length} 套` : '未选择'}</dd></div>
              <div className="flex items-start justify-between gap-4"><dt className="shrink-0 text-base-content/70">姿势</dt><dd className="text-right font-medium wrap-anywhere">{poses.find((p) => p.id === poseId)?.name || (poseId ? '已选择' : '未选择')}</dd></div>
            </dl>
            <div className="divider my-0" />
            <fieldset className="fieldset gap-2 pt-0">
              <legend className="fieldset-legend pb-2 text-sm"><label htmlFor="studio-model">模型</label></legend>
              <select id="studio-model" className="select w-full text-base" value={modelId} disabled={models.length === 0} onChange={(e) => setModelId(e.target.value)}>
                {!models.some((m) => m.model_id === modelId) ? <option value={modelId}>暂无可用模型</option> : null}
                {models.map((m) => (
                  <option key={m.id} value={m.model_id}>{m.provider ? `${m.display_name}（${m.provider}）` : m.display_name}</option>
                ))}
              </select>
              {models.length === 0 ? <div role="status" className="alert alert-warning alert-soft text-sm"><span>暂无可用模型，请联系管理员配置后再生成。</span></div> : null}
            </fieldset>
            <fieldset className="fieldset gap-2 pt-0">
              <legend className="fieldset-legend pb-2 text-sm"><label htmlFor="studio-prompt">补充提示词</label></legend>
              <textarea id="studio-prompt" className="textarea w-full text-base" rows={3} maxLength={500}
                aria-describedby="studio-prompt-count" placeholder="可选：补充风格、光线、背景等要求" value={promptExtra} onChange={(e) => setPromptExtra(e.target.value)} />
              <span id="studio-prompt-count" className="text-right text-xs tabular-nums text-base-content/70">{promptExtra.length}/500</span>
            </fieldset>
            <div className="space-y-3 border-t border-base-300 pt-5" aria-busy={loading}>
              <button type="button" className="btn btn-primary btn-block min-h-12 gap-2" disabled={!canGenerate || loading} onClick={handleGenerate}>
                {loading ? <span className="loading loading-spinner loading-sm" aria-hidden="true" /> : <Sparkles size={18} aria-hidden="true" />}
                {loading ? '生成中…' : '开始生成'}
              </button>
              {err ? (
                <div role="alert" className="alert alert-error alert-soft text-sm">
                  <span className="min-w-0 wrap-anywhere">{err} <Link href="/profile" className="link">查看 App 额度</Link></span>
                  <button type="button" className="btn btn-ghost min-h-11" disabled={!canGenerate || loading} onClick={handleGenerate}>重试</button>
                </div>
              ) : null}
              {!canGenerate && !loading ? (
                <p className="text-center text-sm leading-relaxed text-base-content/70">需要选择 1 个人物 + 至少 1 套服装 + 1 个姿势，并选择可用模型</p>
              ) : null}
              {loading ? <p role="status" className="text-center text-sm text-base-content/70">正在生成换装效果，请稍候…</p> : null}
            </div>
          </div>
        </aside>
      </div>

      <dialog ref={uploadDialog} className="modal" aria-labelledby="studio-upload-title" onClose={() => setCharUploadOpen(false)}>
        <div className="modal-box max-w-lg">
          <div className="mb-5 flex items-center justify-between gap-3">
            <h3 id="studio-upload-title" className="text-xl font-semibold">上传新人物</h3>
            <button type="button" className="btn btn-ghost btn-square" aria-label="关闭上传" onClick={() => setCharUploadOpen(false)}><X size={20} aria-hidden="true" /></button>
          </div>
          {charUploadOpen ? (
            <CharacterUploader onClose={() => setCharUploadOpen(false)} onUploaded={() => { setCharUploadOpen(false); router.refresh(); }} />
          ) : null}
        </div>
        <form method="dialog" className="modal-backdrop"><button type="submit" aria-label="关闭">关闭</button></form>
      </dialog>
    </div>
  );
}

// Suspense 包装：useSearchParams 在静态边界下需要
export function StudioClient(props: Props) {
  return (
    <Suspense fallback={<div role="status" className="alert alert-info alert-soft">加载中…</div>}>
      <StudioClientInner {...props} />
    </Suspense>
  );
}

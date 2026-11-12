'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState, useMemo, useEffect, useRef, useCallback } from 'react';
import { MediaCard } from '@/components/MediaCard';
import { CharacterUploader } from '@/components/CharacterUploader';
import { GarmentUploader } from '@/components/GarmentUploader';
import { QUOTA_PER_UNIT } from '@/lib/constants';
import { LIST_PAGE_SIZE } from '@/lib/list-api';
import { Sparkles, Upload, Search, X, ChevronLeft, ChevronRight } from 'lucide-react';

type Item = { id: string; name: string; subtitle?: string; signedUrl?: string | null };
type OutfitItem = Item & {
  source?: 'official' | 'user';
  parseStatus?: 'pending' | 'parsing' | 'ready' | 'failed';
};

type ModelOption = {
  id: string;
  model_id: string;
  display_name: string;
  provider?: string | null;
  is_default: boolean;
  default_size?: string | null;
  default_quality?: string | null;
};

type QuoteData = {
  credits: number;
  item_count: number;
  unparsed_count: number;
};

type GenerateData = { id: string; result_path?: string | null; result_bucket?: string | null; deduped?: boolean };
type ResultState = { id: string; bucket: string; path: string };
type AdvanceData = {
  status: 'pending' | 'succeeded' | 'failed';
  stage: string;
  result_bucket: string | null;
  result_path: string | null;
  error: string | null;
};

// 推进轮询：每次 /advance 调用本身会阻塞至单步完成，这里的小间隔仅用于避免紧循环。
const ADVANCE_INTERVAL_MS = 1500;
const MAX_ADVANCE_ATTEMPTS = 30;

type Props = {
  outfitCats: string[];
  poseSystems: string[];
  poseAngles: string[];
  initialOutfitQ: string;
  initialOutfitCat: string;
  initialPoseQ: string;
  initialPoseSys: string;
  initialPoseAngle: string;
  models: ModelOption[];
};

const STEPS = ['选择人物', '选择服装', '选择姿势', '确认生成'] as const;
const MAX_OUTFITS = 6;

const SIZE_OPTIONS = [
  { value: 'auto', label: '自动' },
  { value: '1024x1024', label: '1:1 方形（1024×1024）' },
  { value: '1024x1536', label: '2:3 竖版（1024×1536）' },
  { value: '1536x1024', label: '3:2 横版（1536×1024）' },
] as const;
const QUALITY_OPTIONS = [
  { value: 'auto', label: '自动' },
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
] as const;

function defaultSizeOf(v?: string | null): string {
  return v && SIZE_OPTIONS.some((o) => o.value === v) ? v : '1024x1536';
}
function defaultQualityOf(v?: string | null): string {
  return v && v.trim() ? v.trim() : 'auto';
}

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

function formatCredits(value: number): string {
  return `$${(value / QUOTA_PER_UNIT).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

// 平台超时/网关异常可能返回非 JSON（HTML/空响应），直接 res.json() 会抛 SyntaxError 并吞掉提示。
type Envelope = { success?: boolean; ok?: boolean; message?: string; data?: unknown; total?: number };
async function readEnvelope(res: Response): Promise<Envelope> {
  const text = await res.text();
  if (!text) return {};
  try {
    const json: unknown = JSON.parse(text);
    return json && typeof json === 'object' ? json as Envelope : {};
  } catch {
    return {};
  }
}

function failureMessage(res: Response, fallback: string): string {
  if (res.status === 504 || res.status === 503) {
    return '生成耗时较长，服务暂时超时。请稍后在「历史」中查看结果后重试，勿重复提交。';
  }
  return fallback;
}

type ListResult<T> = { items: T[]; total: number };

async function fetchListPage<T>(
  path: string,
  search: URLSearchParams,
  signal: AbortSignal,
  onUnauthorized: () => void,
): Promise<ListResult<T>> {
  const res = await fetch(`${path}?${search.toString()}`, { signal });
  if (res.status === 401) {
    onUnauthorized();
    throw new DOMException('Aborted', 'AbortError');
  }
  const data: Envelope = await readEnvelope(res);
  if (!res.ok || data.ok !== true) {
    throw new Error(typeof data.message === 'string' && data.message ? data.message : '加载失败，请稍后重试');
  }
  return {
    items: Array.isArray(data.data) ? (data.data as T[]) : [],
    total: typeof data.total === 'number' ? data.total : 0,
  };
}

type LoadPage<T> = (page: number, signal: AbortSignal) => Promise<ListResult<T>>;

function usePagedList<T>(load: LoadPage<T>) {
  const [page, setPage] = useState(1);
  const [prevLoad, setPrevLoad] = useState(() => load);
  // 筛选条件变化（load 身份变化）时重置回第一页，避免停留在越界页。
  if (prevLoad !== load) {
    setPrevLoad(load);
    setPage(1);
  }
  const [reloadKey, setReloadKey] = useState(0);
  const [items, setItems] = useState<T[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    setError(null);
    load(page, controller.signal)
      .then((r) => {
        if (!active) return;
        setItems(r.items);
        setTotal(r.total);
      })
      .catch((e: unknown) => {
        if (!active) return;
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [load, page, reloadKey]);

  const totalPages = Math.max(1, Math.ceil(total / LIST_PAGE_SIZE));
  const reload = () => {
    setPage(1);
    setReloadKey((k) => k + 1);
  };

  return { items, total, totalPages, page, setPage, loading, error, reload };
}

function Pager({
  page, totalPages, total, onPage,
}: { page: number; totalPages: number; total: number; onPage: (p: number) => void }) {
  if (totalPages <= 1) return null;
  const firstPage = Math.min(Math.max(1, page - 3), Math.max(1, totalPages - 6));
  return (
    <nav aria-label="分页" className="flex flex-wrap justify-center gap-1">
      <button type="button" className="btn btn-sm btn-outline" disabled={page <= 1} onClick={() => onPage(page - 1)}>上一页</button>
      {Array.from({ length: Math.min(7, totalPages) }, (_, i) => firstPage + i).map((p) => (
        <button
          key={p}
          type="button"
          aria-current={p === page ? 'page' : undefined}
          className={`btn btn-sm ${p === page ? 'btn-primary' : 'btn-outline'}`}
          onClick={() => onPage(p)}
        >
          {p}
        </button>
      ))}
      <button type="button" className="btn btn-sm btn-outline" disabled={page >= totalPages} onClick={() => onPage(page + 1)}>下一页</button>
      <span className="self-center px-2 text-sm">第 {page} / {totalPages} 页 · 共 {total} 个</span>
    </nav>
  );
}

export function StudioClient({
  outfitCats, poseSystems, poseAngles,
  initialOutfitQ, initialOutfitCat,
  initialPoseQ, initialPoseSys, initialPoseAngle,
  models,
}: Props) {
  const router = useRouter();

  const [step, setStep] = useState(1);
  const [maxStep, setMaxStep] = useState(1);

  const [selectedCharacter, setSelectedCharacter] = useState<Item | null>(null);
  const [selectedOutfits, setSelectedOutfits] = useState<OutfitItem[]>([]);
  const [selectedPose, setSelectedPose] = useState<Item | null>(null);
  const charId = selectedCharacter?.id ?? null;
  const outfitIds = selectedOutfits.map((o) => o.id);
  const poseId = selectedPose?.id ?? null;
  const defaultModel = models.find((m) => m.is_default) ?? models[0] ?? null;
  const [modelId, setModelId] = useState(defaultModel?.model_id ?? '');
  const [size, setSize] = useState(defaultSizeOf(defaultModel?.default_size));
  const [quality, setQuality] = useState(defaultQualityOf(defaultModel?.default_quality));
  const [promptExtra, setPromptExtra] = useState('');

  // 服装筛选（纯本地，不再驱动 URL）
  const [outfitTab, setOutfitTab] = useState<'all' | 'official' | 'mine'>('all');
  const [outfitQ, setOutfitQ] = useState(initialOutfitQ);
  const [outfitCat, setOutfitCat] = useState(initialOutfitCat);
  // 姿势筛选
  const [poseQ, setPoseQ] = useState(initialPoseQ);
  const [poseSys, setPoseSys] = useState(initialPoseSys);
  const [poseAngle, setPoseAngle] = useState(initialPoseAngle);

  const [charUploadOpen, setCharUploadOpen] = useState(false);
  const [garmentUploadOpen, setGarmentUploadOpen] = useState(false);
  const charDialog = useRef<HTMLDialogElement>(null);
  const garmentDialog = useRef<HTMLDialogElement>(null);

  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [result, setResult] = useState<ResultState | null>(null);
  const [quote, setQuote] = useState<QuoteData | null>(null);
  const [quoteLoading, setQuoteLoading] = useState(false);
  const [quoteErr, setQuoteErr] = useState<string | null>(null);

  // 同一向导会话内保持稳定，重试也复用同一幂等键，避免重复下单
  const [idempotencyKey] = useState(() => crypto.randomUUID());

  const handleUnauthorized = useCallback(() => {
    router.replace('/login?error=session_expired');
  }, [router]);

  const loadCharacters = useCallback((page: number, signal: AbortSignal) => {
    const search = new URLSearchParams({ page: String(page), limit: String(LIST_PAGE_SIZE) });
    return fetchListPage<Item>('/api/characters', search, signal, handleUnauthorized);
  }, [handleUnauthorized]);

  const loadOutfits = useCallback((page: number, signal: AbortSignal) => {
    const search = new URLSearchParams({ page: String(page), limit: String(LIST_PAGE_SIZE) });
    if (outfitTab === 'official') search.set('source', 'official');
    else if (outfitTab === 'mine') search.set('source', 'user');
    if (outfitCat) search.set('category', outfitCat);
    if (outfitQ.trim()) search.set('q', outfitQ.trim());
    return fetchListPage<OutfitItem>('/api/outfits', search, signal, handleUnauthorized);
  }, [outfitTab, outfitCat, outfitQ, handleUnauthorized]);

  const loadPoses = useCallback((page: number, signal: AbortSignal) => {
    const search = new URLSearchParams({ page: String(page), limit: String(LIST_PAGE_SIZE) });
    if (poseSys) search.set('system', poseSys);
    if (poseAngle) search.set('angle', poseAngle);
    if (poseQ.trim()) search.set('q', poseQ.trim());
    return fetchListPage<Item>('/api/poses', search, signal, handleUnauthorized);
  }, [poseSys, poseAngle, poseQ, handleUnauthorized]);

  const characters = usePagedList<Item>(loadCharacters);
  const outfits = usePagedList<OutfitItem>(loadOutfits);
  const poses = usePagedList<Item>(loadPoses);

  useEffect(() => {
    if (charUploadOpen) charDialog.current?.showModal();
    else charDialog.current?.close();
  }, [charUploadOpen]);

  useEffect(() => {
    if (garmentUploadOpen) garmentDialog.current?.showModal();
    else garmentDialog.current?.close();
  }, [garmentUploadOpen]);

  const selectedModel = models.find((m) => m.model_id === modelId) ?? null;

  const qualityOptions = useMemo(() => {
    const opts: { value: string; label: string }[] = QUALITY_OPTIONS.map((o) => ({ value: o.value, label: o.label }));
    const dq = selectedModel?.default_quality?.trim();
    if (dq && !opts.some((o) => o.value === dq)) opts.push({ value: dq, label: dq });
    return opts;
  }, [selectedModel]);

  const toggleOutfit = (item: OutfitItem) => {
    setSelectedOutfits((prev) => {
      if (prev.some((o) => o.id === item.id)) return prev.filter((o) => o.id !== item.id);
      if (prev.length >= MAX_OUTFITS) return prev;
      return [...prev, item];
    });
  };

  const stepValid = (s: number): boolean => {
    if (s === 1) return Boolean(charId);
    if (s === 2) return outfitIds.length > 0;
    if (s === 3) return Boolean(poseId);
    return true;
  };

  function goToStep(target: number) {
    if (target < 1 || target > STEPS.length) return;
    setStep(target);
    setMaxStep((prev) => Math.max(prev, target));
    // 进入确认步骤时拉取一次报价；该步骤素材不可改，回退再进入会按当前选择重新核算
    if (target === 4) void loadQuote();
  }

  async function loadQuote() {
    if (!charId || outfitIds.length === 0 || !poseId) return;
    setQuoteLoading(true);
    setQuoteErr(null);
    try {
      const res = await fetch('/api/generate/quote', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ character_id: charId, outfit_ids: outfitIds, pose_id: poseId }),
      });
      const data: Envelope = await readEnvelope(res);
      if (res.status === 401) {
        router.replace('/login?error=session_expired');
        return;
      }
      if (!res.ok || !data.success || !data.data) {
        throw new Error(typeof data.message === 'string' && data.message
          ? data.message
          : failureMessage(res, '报价获取失败，请稍后重试'));
      }
      setQuote(data.data as QuoteData);
    } catch (e) {
      setQuoteErr(e instanceof Error ? e.message : String(e));
    } finally {
      setQuoteLoading(false);
    }
  }

  async function handleGenerate() {
    if (loading || !charId || outfitIds.length === 0 || !poseId) return;
    if (!models.some((m) => m.model_id === modelId)) return;
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
          size,
          quality,
          prompt_extra: promptExtra.trim() || undefined,
          idempotency_key: idempotencyKey,
        }),
      });
      const data: Envelope = await readEnvelope(res);
      if (res.status === 401) {
        router.replace('/login?error=session_expired');
        return;
      }
      if (!res.ok || !data.success || !data.data) {
        throw new Error(typeof data.message === 'string' && data.message
          ? data.message
          : failureMessage(res, '生成失败，请稍后重试'));
      }
      const created = data.data as GenerateData;
      // 幂等命中且已有结果：直接展示结果，不再推进。
      if (created.result_path && created.result_bucket) {
        setResult({ id: created.id, bucket: created.result_bucket, path: created.result_path });
        return;
      }
      // 创建任务后由前端逐次调用 /advance 单步推进，避开 Hobby 60s 硬超时。
      const adv = await pollUntilDone(created.id);
      if (adv?.result_path && adv.result_bucket) {
        setResult({ id: created.id, bucket: adv.result_bucket, path: adv.result_path });
      } else if (adv) {
        // 成功但缺少结果路径（异常情况）：退回历史页查看记录。
        router.push('/history');
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  async function pollUntilDone(id: string): Promise<AdvanceData | null> {
    for (let i = 0; i < MAX_ADVANCE_ATTEMPTS; i += 1) {
      await new Promise((resolve) => setTimeout(resolve, ADVANCE_INTERVAL_MS));
      const res = await fetch(`/api/generate/${id}/advance`, { method: 'POST' });
      const data: Envelope = await readEnvelope(res);
      if (res.status === 401) {
        router.replace('/login?error=session_expired');
        return null;
      }
      if (!res.ok || !data.success || !data.data) {
        throw new Error(typeof data.message === 'string' && data.message
          ? data.message
          : failureMessage(res, '生成过程中断，请稍后在「历史」中查看结果'));
      }
      const adv = data.data as AdvanceData;
      if (adv.status === 'succeeded') return adv;
      if (adv.status === 'failed') throw new Error(adv.error || '生成失败，请稍后重试');
      // pending：继续轮询
    }
    throw new Error('生成耗时较长，请稍后在「历史」中查看结果，勿重复提交。');
  }

  const modelReady = models.some((m) => m.model_id === modelId);

  return (
    <div className="mx-auto w-full max-w-5xl space-y-6">
      <header className="space-y-4">
        <div className="space-y-2">
          <span className="badge badge-outline gap-2"><Sparkles size={14} aria-hidden="true" /> AI 换装</span>
          <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">换装工作室</h1>
          <p className="text-base leading-relaxed text-base-content/70">按步骤选择人物、服装和姿势，确认报价后生成换装效果。</p>
        </div>
        <ul className="steps w-full text-sm" aria-label="换装流程">
          {STEPS.map((label, i) => {
            const idx = i + 1;
            const reached = idx <= maxStep;
            const active = idx === step;
            return (
              <li key={label} className={`step ${idx <= step ? 'step-primary' : ''}`}>
                <button
                  type="button"
                  disabled={!reached}
                  aria-current={active ? 'step' : undefined}
                  className="link link-hover disabled:no-underline disabled:opacity-60"
                  onClick={() => goToStep(idx)}
                >
                  {label}
                </button>
              </li>
            );
          })}
        </ul>
      </header>

      {result ? (
        <section className="card card-border bg-base-100 shadow-md" aria-labelledby="result-title">
          <div className="card-body items-center gap-5 p-4 text-center sm:p-8">
            <h2 id="result-title" className="card-title text-xl"><span className="badge badge-success">完成</span>生成完成</h2>
            <div className="w-full max-w-md overflow-hidden rounded-box bg-base-200">
              {/* 结果图走媒体代理动态签名，不能用 next/image */}
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={`/api/media/${result.bucket}/${result.path}`} alt="换装结果" className="h-auto w-full object-contain" />
            </div>
            <div className="flex flex-wrap justify-center gap-2">
              <Link href="/history" className="btn btn-primary min-h-11 gap-2">去作品历史 <ChevronRight size={16} aria-hidden="true" /></Link>
              <button type="button" className="btn btn-ghost min-h-11" onClick={() => { setResult(null); setStep(1); }}>再生成一次</button>
            </div>
          </div>
        </section>
      ) : (
        <>
      {/* Step 1 — 人物 */}
      {step === 1 ? (
        <section className="card card-border bg-base-100 shadow-sm" aria-labelledby="step-1-title">
          <div className="card-body gap-5 p-4 sm:p-6">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 id="step-1-title" className="card-title text-xl"><span className="badge badge-primary">1</span>选择人物</h2>
              <span className="badge badge-outline">{characters.total} 个</span>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <button type="button" className="btn btn-outline min-h-11 gap-2" onClick={() => setCharUploadOpen(true)}>
                <Upload size={16} aria-hidden="true" />上传新人物
              </button>
              <Link href="/characters" className="btn btn-ghost min-h-11 gap-1">去人物库 <ChevronRight size={16} aria-hidden="true" /></Link>
            </div>
            {characters.loading && characters.items.length === 0 ? (
              <div className="flex justify-center py-8"><span className="loading loading-spinner loading-lg" aria-hidden="true" /><span className="sr-only">加载中</span></div>
            ) : characters.error ? (
              <div role="alert" className="alert alert-error alert-soft"><span className="min-w-0 wrap-anywhere">{characters.error}</span></div>
            ) : characters.items.length > 0 ? (
              <>
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 xl:grid-cols-4">
                  {characters.items.map((c) => (
                    <MediaCard key={c.id} title={c.name} signedUrl={c.signedUrl} variant="tile"
                      selected={selectedCharacter?.id === c.id} onSelect={() => setSelectedCharacter(c.id === selectedCharacter?.id ? null : c)} />
                  ))}
                </div>
                <Pager page={characters.page} totalPages={characters.totalPages} total={characters.total} onPage={characters.setPage} />
              </>
            ) : (
              <div className="card card-border border-dashed bg-base-200 text-base-content">
                <div className="card-body items-center gap-3 py-8 text-center">
                  <p className="text-base-content/70">还没有上传过人物，先上传一张人物图片吧</p>
                  <button type="button" className="btn btn-outline gap-2" onClick={() => setCharUploadOpen(true)}><Upload size={16} aria-hidden="true" />上传人物</button>
                </div>
              </div>
            )}
            <div className="flex items-center justify-between gap-3 border-t border-base-300 pt-4">
              <span className="text-sm text-base-content/70">{charId ? `已选择：${selectedCharacter?.name}` : '请选择 1 个人物'}</span>
              <button type="button" className="btn btn-primary min-h-11 gap-2" disabled={!stepValid(1)} onClick={() => goToStep(2)}>
                下一步<ChevronRight size={16} aria-hidden="true" />
              </button>
            </div>
          </div>
        </section>
      ) : null}

      {/* Step 2 — 服装 */}
      {step === 2 ? (
        <section className="card card-border bg-base-100 shadow-sm" aria-labelledby="step-2-title">
          <div className="card-body gap-5 p-4 sm:p-6">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 id="step-2-title" className="card-title text-xl"><span className="badge badge-primary">2</span>选择服装</h2>
              <span className="badge badge-outline tabular-nums" aria-live="polite">已选 {outfitIds.length} / {MAX_OUTFITS} 套</span>
            </div>

            <div role="tablist" aria-label="服装来源" className="tabs tabs-box tabs-sm self-start">
              {([['all', '全部'], ['official', '官方素材'], ['mine', '我的服装']] as const).map(([value, label]) => (
                <button
                  key={value}
                  role="tab"
                  type="button"
                  aria-selected={outfitTab === value}
                  className={`tab ${outfitTab === value ? 'tab-active' : ''}`}
                  onClick={() => setOutfitTab(value)}
                >
                  {label}
                </button>
              ))}
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <button type="button" className="btn btn-outline min-h-11 gap-2" onClick={() => setGarmentUploadOpen(true)}>
                <Upload size={16} aria-hidden="true" />上传我的服装
              </button>
              <Link href="/wardrobe" className="btn btn-ghost min-h-11 gap-1">服装库 <ChevronRight size={16} aria-hidden="true" /></Link>
            </div>

            <label className="input min-w-0 gap-2">
              <Search size={18} className="shrink-0 text-base-content/70" aria-hidden="true" />
              <input type="search" className="grow text-base" aria-label="搜索服装名称/描述" placeholder="搜索服装名称/描述"
                value={outfitQ} onChange={(e) => setOutfitQ(e.target.value)} />
              {outfitQ ? (
                <button type="button" className="btn btn-ghost btn-circle btn-xs" aria-label="清空搜索" onClick={() => setOutfitQ('')}><X size={14} /></button>
              ) : null}
            </label>

            {outfitCats.length > 0 ? (
              <div className="flex flex-wrap gap-2" aria-label="服装分类">
                <Chip active={!outfitCat} onClick={() => setOutfitCat('')}>全部分类</Chip>
                {outfitCats.map((c) => (
                  <Chip key={c} active={outfitCat === c} onClick={() => setOutfitCat(outfitCat === c ? '' : c)}>{c}</Chip>
                ))}
              </div>
            ) : null}

            {outfits.loading && outfits.items.length === 0 ? (
              <div className="flex justify-center py-8"><span className="loading loading-spinner loading-lg" aria-hidden="true" /><span className="sr-only">加载中</span></div>
            ) : outfits.error ? (
              <div role="alert" className="alert alert-error alert-soft"><span className="min-w-0 wrap-anywhere">{outfits.error}</span></div>
            ) : outfits.items.length > 0 ? (
              <>
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 xl:grid-cols-4">
                  {outfits.items.map((o) => (
                    <MediaCard
                      key={o.id}
                      title={o.name}
                      subtitle={o.subtitle}
                      signedUrl={o.signedUrl}
                      variant="tile"
                      badge={o.source === 'user' ? '我的' : '官方'}
                      selected={outfitIds.includes(o.id)}
                      disabled={outfitIds.length >= MAX_OUTFITS && !outfitIds.includes(o.id)}
                      onSelect={() => toggleOutfit(o)}
                    />
                  ))}
                </div>
                <Pager page={outfits.page} totalPages={outfits.totalPages} total={outfits.total} onPage={outfits.setPage} />
              </>
            ) : (
              <div className="card card-border border-dashed bg-base-200 text-base-content">
                <div className="card-body items-center gap-3 py-8 text-center">
                  <p className="text-base-content/70">{outfitTab === 'mine' ? '还没有上传过服装，上传后仅你自己可见' : '没有匹配的服装'}</p>
                  {outfitTab === 'mine' ? (
                    <button type="button" className="btn btn-outline gap-2" onClick={() => setGarmentUploadOpen(true)}><Upload size={16} aria-hidden="true" />上传服装</button>
                  ) : (
                    <Link href="/wardrobe" className="btn btn-outline">去服装库</Link>
                  )}
                </div>
              </div>
            )}
            <p className="text-xs text-base-content/60">个人服装上传即可用；首次用于生成时会自动完成单件解析，官方素材与已解析服装无需等待。</p>
            {outfitIds.length >= MAX_OUTFITS ? <p role="status" className="text-sm text-base-content/70">最多选择 {MAX_OUTFITS} 套服装，取消一套后可继续选择。</p> : null}

            <div className="flex items-center justify-between gap-3 border-t border-base-300 pt-4">
              <button type="button" className="btn btn-ghost min-h-11 gap-2" onClick={() => goToStep(1)}>
                <ChevronLeft size={16} aria-hidden="true" />上一步
              </button>
              <button type="button" className="btn btn-primary min-h-11 gap-2" disabled={!stepValid(2)} onClick={() => goToStep(3)}>
                下一步<ChevronRight size={16} aria-hidden="true" />
              </button>
            </div>
          </div>
        </section>
      ) : null}

      {/* Step 3 — 姿势 */}
      {step === 3 ? (
        <section className="card card-border bg-base-100 shadow-sm" aria-labelledby="step-3-title">
          <div className="card-body gap-5 p-4 sm:p-6">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 id="step-3-title" className="card-title text-xl"><span className="badge badge-primary">3</span>选择姿势</h2>
              <span className="badge badge-outline">{poses.total} 个</span>
            </div>
            <label className="input min-w-0 gap-2">
              <Search size={18} className="shrink-0 text-base-content/70" aria-hidden="true" />
              <input type="search" className="grow text-base" aria-label="搜索姿势" placeholder="搜索姿势"
                value={poseQ} onChange={(e) => setPoseQ(e.target.value)} />
              {poseQ ? (
                <button type="button" className="btn btn-ghost btn-circle btn-xs" aria-label="清空搜索" onClick={() => setPoseQ('')}><X size={14} /></button>
              ) : null}
            </label>
            {poseSystems.length > 0 ? (
              <div className="flex flex-wrap gap-2" aria-label="姿势系统">
                <Chip active={!poseSys} onClick={() => setPoseSys('')}>全部系统</Chip>
                {poseSystems.slice(0, 6).map((s) => (
                  <Chip key={s} active={poseSys === s} onClick={() => setPoseSys(poseSys === s ? '' : s)}>{s}</Chip>
                ))}
              </div>
            ) : null}
            {poseAngles.length > 0 ? (
              <div className="flex flex-wrap gap-2" aria-label="姿势角度">
                <Chip active={!poseAngle} onClick={() => setPoseAngle('')}>全部角度</Chip>
                {poseAngles.slice(0, 8).map((a) => (
                  <Chip key={a} active={poseAngle === a} onClick={() => setPoseAngle(poseAngle === a ? '' : a)}>{a}</Chip>
                ))}
              </div>
            ) : null}
            {poses.loading && poses.items.length === 0 ? (
              <div className="flex justify-center py-8"><span className="loading loading-spinner loading-lg" aria-hidden="true" /><span className="sr-only">加载中</span></div>
            ) : poses.error ? (
              <div role="alert" className="alert alert-error alert-soft"><span className="min-w-0 wrap-anywhere">{poses.error}</span></div>
            ) : poses.items.length > 0 ? (
              <>
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 xl:grid-cols-4">
                  {poses.items.map((p) => (
                    <MediaCard key={p.id} title={p.name} subtitle={p.subtitle} signedUrl={p.signedUrl} variant="tile"
                      selected={selectedPose?.id === p.id} onSelect={() => setSelectedPose(p.id === selectedPose?.id ? null : p)} />
                  ))}
                </div>
                <Pager page={poses.page} totalPages={poses.totalPages} total={poses.total} onPage={poses.setPage} />
              </>
            ) : (
              <div className="card card-border border-dashed bg-base-200 text-base-content">
                <div className="card-body items-center gap-3 py-8 text-center">
                  <p className="text-base-content/70">没有匹配的姿势</p>
                  <Link href="/poses" className="btn btn-outline">去姿势库</Link>
                </div>
              </div>
            )}
            <div className="flex items-center justify-between gap-3 border-t border-base-300 pt-4">
              <button type="button" className="btn btn-ghost min-h-11 gap-2" onClick={() => goToStep(2)}>
                <ChevronLeft size={16} aria-hidden="true" />上一步
              </button>
              <button type="button" className="btn btn-primary min-h-11 gap-2" disabled={!stepValid(3)} onClick={() => goToStep(4)}>
                下一步<ChevronRight size={16} aria-hidden="true" />
              </button>
            </div>
          </div>
        </section>
      ) : null}

      {/* Step 4 — 确认与报价 */}
      {step === 4 ? (
        <section className="card card-border bg-base-100 shadow-md" aria-labelledby="step-4-title">
          <div className="card-body gap-5 p-4 sm:p-6">
            <h2 id="step-4-title" className="card-title text-xl"><span className="badge badge-primary">4</span>确认并生成</h2>

            <dl className="grid gap-3 text-sm sm:grid-cols-3" aria-live="polite">
              <div className="card card-border bg-base-200">
                <div className="card-body gap-1 p-4">
                  <dt className="text-xs text-base-content/60">人物</dt>
                  <dd className="font-medium wrap-anywhere">{selectedCharacter?.name ?? '未选择'}</dd>
                </div>
              </div>
              <div className="card card-border bg-base-200">
                <div className="card-body gap-1 p-4">
                  <dt className="text-xs text-base-content/60">服装（{selectedOutfits.length} 套）</dt>
                  <dd className="line-clamp-3 font-medium wrap-anywhere" title={selectedOutfits.map((o) => o.name).join('、')}>
                    {selectedOutfits.map((o) => o.name).join('、') || '未选择'}
                  </dd>
                </div>
              </div>
              <div className="card card-border bg-base-200">
                <div className="card-body gap-1 p-4">
                  <dt className="text-xs text-base-content/60">姿势</dt>
                  <dd className="font-medium wrap-anywhere">{selectedPose?.name ?? '未选择'}</dd>
                </div>
              </div>
            </dl>

            <div className="card card-border border-primary/40 bg-primary/5">
              <div className="card-body gap-2 p-4">
                <h3 className="font-medium">本次报价</h3>
                {quoteLoading ? (
                  <p role="status" className="flex items-center gap-2 text-sm text-base-content/70">
                    <span className="loading loading-spinner loading-sm" aria-hidden="true" />正在核算报价…
                  </p>
                ) : quoteErr ? (
                  <div role="alert" className="alert alert-error alert-soft text-sm">
                    <span className="min-w-0 wrap-anywhere">{quoteErr}</span>
                    <button type="button" className="btn btn-ghost min-h-11" onClick={loadQuote}>重试</button>
                  </div>
                ) : quote ? (
                  <div className="space-y-1 text-sm">
                    <p>预计消耗：<strong className="text-lg tabular-nums">{formatCredits(quote.credits)}</strong></p>
                    <p className="text-base-content/70">共 {quote.item_count} 套服装，其中 {quote.unparsed_count} 套个人服装将在生成时自动解析。</p>
                    <p className="text-xs text-base-content/60">最终以网关按模型实际用量计费为准；报价仅用于提交前确认，重复提交同一选择不会重复生成。</p>
                  </div>
                ) : (
                  <p className="text-sm text-base-content/70">暂无报价</p>
                )}
              </div>
            </div>

            <fieldset className="fieldset gap-2 pt-0">
              <legend className="fieldset-legend pb-2 text-sm"><label htmlFor="studio-model">模型</label></legend>
              <select id="studio-model" className="select w-full text-base" value={modelId} disabled={models.length === 0} onChange={(e) => {
                const next = e.target.value;
                setModelId(next);
                const m = models.find((x) => x.model_id === next);
                setSize(defaultSizeOf(m?.default_size));
                setQuality(defaultQualityOf(m?.default_quality));
              }}>
                {!modelReady ? <option value={modelId}>暂无可用模型</option> : null}
                {models.map((m) => (
                  <option key={m.id} value={m.model_id}>{m.provider ? `${m.display_name}（${m.provider}）` : m.display_name}</option>
                ))}
              </select>
              {models.length === 0 ? <div role="status" className="alert alert-warning alert-soft text-sm"><span>暂无可用模型，请联系管理员配置后再生成。</span></div> : null}
            </fieldset>

            <div className="grid gap-4 sm:grid-cols-2">
              <fieldset className="fieldset gap-2 pt-0">
                <legend className="fieldset-legend pb-2 text-sm"><label htmlFor="studio-size">图片尺寸</label></legend>
                <select id="studio-size" className="select w-full text-base" value={size} onChange={(e) => setSize(e.target.value)}>
                  {SIZE_OPTIONS.map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </select>
              </fieldset>
              <fieldset className="fieldset gap-2 pt-0">
                <legend className="fieldset-legend pb-2 text-sm"><label htmlFor="studio-quality">图片质量</label></legend>
                <select id="studio-quality" className="select w-full text-base" value={quality} onChange={(e) => setQuality(e.target.value)}>
                  {qualityOptions.map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </select>
              </fieldset>
            </div>

            <fieldset className="fieldset gap-2 pt-0">
              <legend className="fieldset-legend pb-2 text-sm"><label htmlFor="studio-prompt">补充提示词</label></legend>
              <textarea id="studio-prompt" className="textarea w-full text-base" rows={3} maxLength={500}
                aria-describedby="studio-prompt-count" placeholder="可选：补充风格、光线、背景等要求" value={promptExtra} onChange={(e) => setPromptExtra(e.target.value)} />
              <span id="studio-prompt-count" className="text-right text-xs tabular-nums text-base-content/70">{promptExtra.length}/500</span>
            </fieldset>

            {err ? (
              <div role="alert" className="alert alert-error alert-soft text-sm">
                <span className="min-w-0 wrap-anywhere">{err} <Link href="/profile" className="link">查看余额</Link></span>
                <button type="button" className="btn btn-ghost min-h-11" disabled={loading || !modelReady} onClick={handleGenerate}>重试</button>
              </div>
            ) : null}

            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-base-300 pt-4">
              <button type="button" className="btn btn-ghost min-h-11 gap-2" onClick={() => goToStep(3)}>
                <ChevronLeft size={16} aria-hidden="true" />上一步
              </button>
              <button type="button" className="btn btn-primary min-h-12 gap-2" disabled={loading || !modelReady || quoteLoading} onClick={handleGenerate}>
                {loading ? <span className="loading loading-spinner loading-sm" aria-hidden="true" /> : <Sparkles size={18} aria-hidden="true" />}
                {loading ? '生成中…' : '确认生成'}
              </button>
            </div>
            {loading ? <p role="status" className="text-center text-sm text-base-content/70">正在生成换装效果，请稍候，期间请勿关闭页面…</p> : null}
          </div>
        </section>
      ) : null}

        </>
      )}

      <dialog ref={charDialog} className="modal" aria-labelledby="char-upload-title" onClose={() => setCharUploadOpen(false)}>
        <div className="modal-box max-w-lg">
          <div className="mb-5 flex items-center justify-between gap-3">
            <h3 id="char-upload-title" className="text-xl font-semibold">上传新人物</h3>
            <button type="button" className="btn btn-ghost btn-square" aria-label="关闭上传" onClick={() => setCharUploadOpen(false)}><X size={20} aria-hidden="true" /></button>
          </div>
          {charUploadOpen ? (
            <CharacterUploader onClose={() => setCharUploadOpen(false)} onUploaded={() => { setCharUploadOpen(false); characters.reload(); }} />
          ) : null}
        </div>
        <form method="dialog" className="modal-backdrop"><button type="submit" aria-label="关闭">关闭</button></form>
      </dialog>

      <dialog ref={garmentDialog} className="modal" aria-labelledby="garment-upload-title" onClose={() => setGarmentUploadOpen(false)}>
        <div className="modal-box max-w-lg">
          <div className="mb-5 flex items-center justify-between gap-3">
            <h3 id="garment-upload-title" className="text-xl font-semibold">上传我的服装</h3>
            <button type="button" className="btn btn-ghost btn-square" aria-label="关闭上传" onClick={() => setGarmentUploadOpen(false)}><X size={20} aria-hidden="true" /></button>
          </div>
          {garmentUploadOpen ? (
            <GarmentUploader
              onClose={() => setGarmentUploadOpen(false)}
              onUploaded={() => { setGarmentUploadOpen(false); setOutfitTab('mine'); outfits.reload(); }}
            />
          ) : null}
        </div>
        <form method="dialog" className="modal-backdrop"><button type="submit" aria-label="关闭">关闭</button></form>
      </dialog>
    </div>
  );
}

'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Plus, Trash2, ImageOff } from 'lucide-react';
import { GarmentUploader } from '@/components/GarmentUploader';

export interface PersonalGarmentItem {
  id: string;
  name_zh: string;
  category: string;
  description_zh: string | null;
  parse_status: 'pending' | 'parsing' | 'ready' | 'failed';
  created_at: string;
  signed_url?: string;
}

type Props = {
  initialItems: PersonalGarmentItem[];
};

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString('zh-CN');
  } catch {
    return iso;
  }
}

const STATUS_LABEL: Record<PersonalGarmentItem['parse_status'], { text: string; cls: string }> = {
  pending: { text: '待解析', cls: 'badge-warning' },
  parsing: { text: '解析中', cls: 'badge-info' },
  ready: { text: '可换装', cls: 'badge-success' },
  failed: { text: '解析失败', cls: 'badge-error' },
};

export function PersonalWardrobeClient({ initialItems }: Props) {
  const router = useRouter();
  const [serverItems, setServerItems] = useState(initialItems);
  const [items, setItems] = useState<PersonalGarmentItem[]>(initialItems);
  if (initialItems !== serverItems) {
    setServerItems(initialItems);
    setItems(initialItems);
  }
  const [uploadOpen, setUploadOpen] = useState(false);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const uploadDialog = useRef<HTMLDialogElement>(null);
  const deleteDialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    for (const [dialog, open] of [
      [uploadDialog.current, uploadOpen],
      [deleteDialog.current, deletingId !== null],
    ] as const) {
      if (open && !dialog?.open) dialog?.showModal();
      if (!open && dialog?.open) dialog.close();
    }
  }, [uploadOpen, deletingId]);

  const refresh = useCallback(() => {
    router.refresh();
  }, [router]);

  async function handleDelete(id: string) {
    setErr(null);
    try {
      const res = await fetch(`/api/outfits?id=${encodeURIComponent(id)}`, { method: 'DELETE' });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message ?? `HTTP ${res.status}`);
      setItems((prev) => prev.filter((x) => x.id !== id));
      setDeletingId(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <h2 className="text-xl font-bold">我的服装</h2>
          <span className="badge badge-ghost">{items.length} 件</span>
        </div>
        <button
          type="button"
          className="btn btn-primary btn-sm gap-2"
          onClick={() => { setUploadOpen(true); setErr(null); }}
        >
          <Plus size={16} />上传服装
        </button>
      </div>

      <p className="text-sm text-base-content/60">
        上传的服装仅自己可见，会在首次换装时自动解析为可制作素材；官方服装库的素材所有人共享，不会变成你的私有服装。
      </p>

      {err ? <div role="alert" className="alert alert-error alert-soft text-sm">{err}</div> : null}

      {items.length === 0 ? (
        <div className="alert alert-info">
          <div>
            <h3 className="font-bold">还没有个人服装</h3>
            <p>上传一件衣服图片，建立只属于你的衣橱。</p>
          </div>
          <button type="button" className="btn btn-primary btn-sm" onClick={() => setUploadOpen(true)}>
            开始上传
          </button>
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {items.map((o) => {
            const status = STATUS_LABEL[o.parse_status] ?? STATUS_LABEL.pending;
            return (
              <div key={o.id} className="card card-sm bg-base-100 shadow-sm">
                <figure className="relative aspect-[3/4] w-full overflow-hidden bg-base-200">
                  {o.signed_url ? (
                    // eslint-disable-next-line @next/next/no-img-element -- signed URL 动态生成
                    <img src={o.signed_url} alt={o.name_zh} className="h-full w-full object-cover" />
                  ) : (
                    <div className="flex h-full w-full items-center justify-center text-base-content/40">
                      <ImageOff size={24} />
                    </div>
                  )}
                  <span className={`badge badge-sm absolute left-2 top-2 ${status.cls}`}>{status.text}</span>
                </figure>
                <div className="card-body">
                  <h3 className="card-title text-sm">{o.name_zh}</h3>
                  <p className="text-xs text-base-content/50">{o.category}</p>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-base-content/40">{formatDate(o.created_at)}</span>
                    <button
                      type="button"
                      className="btn btn-ghost btn-xs btn-square text-error"
                      aria-label="删除"
                      onClick={() => setDeletingId(o.id)}
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}

      <dialog ref={uploadDialog} className="modal" aria-label="上传服装" onClose={() => setUploadOpen(false)}>
        <div className="modal-box max-w-lg">
          <h3 className="mb-3 text-lg font-bold">上传新服装</h3>
          {uploadOpen ? (
            <GarmentUploader
              onClose={() => setUploadOpen(false)}
              onUploaded={() => { setUploadOpen(false); refresh(); }}
            />
          ) : null}
        </div>
      </dialog>

      <dialog ref={deleteDialog} className="modal" aria-label="确认删除" onClose={() => setDeletingId(null)}>
        <div className="modal-box max-w-sm">
          <h3 className="mb-2 text-lg font-bold">确认删除？</h3>
          <p className="text-sm text-base-content/70">删除后无法恢复，原图与已解析素材会一并清除。</p>
          <div className="modal-action">
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => setDeletingId(null)}>取消</button>
            <button
              type="button"
              className="btn btn-sm btn-error"
              onClick={() => { if (deletingId) void handleDelete(deletingId); }}
            >
              删除
            </button>
          </div>
        </div>
      </dialog>
    </div>
  );
}

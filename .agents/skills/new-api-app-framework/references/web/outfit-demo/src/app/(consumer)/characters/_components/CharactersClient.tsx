'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Plus, Trash2, Pencil, ImageOff } from 'lucide-react';
import { CharacterUploader } from '@/components/CharacterUploader';

interface CharacterItem {
  id: string;
  name: string;
  description: string | null;
  is_public: boolean;
  created_at: string;
  signed_url?: string;
}

type Props = {
  initialItems: CharacterItem[];
  uploaderSlot?: React.ReactNode;
};

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString('zh-CN');
  } catch {
    return iso;
  }
}

export function CharactersClient({ initialItems }: Props) {
  const router = useRouter();
  const [serverItems, setServerItems] = useState(initialItems);
  const [items, setItems] = useState<CharacterItem[]>(initialItems);
  // Refresh merges new server props without resetting local state. Keep the list current,
  // but leave the independent edit draft intact.
  if (initialItems !== serverItems) {
    setServerItems(initialItems);
    setItems(initialItems);
  }
  const [uploadOpen, setUploadOpen] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState('');
  const [editDesc, setEditDesc] = useState('');
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const uploadDialog = useRef<HTMLDialogElement>(null);
  const editDialog = useRef<HTMLDialogElement>(null);
  const deleteDialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    for (const [dialog, open] of [
      [uploadDialog.current, uploadOpen],
      [editDialog.current, editingId !== null],
      [deleteDialog.current, deletingId !== null],
    ] as const) {
      if (open && !dialog?.open) dialog?.showModal();
      if (!open && dialog?.open) dialog.close();
    }
  }, [uploadOpen, editingId, deletingId]);

  const refresh = useCallback(() => {
    router.refresh();
  }, [router]);

  async function handleDelete(id: string) {
    setErr(null);
    try {
      const res = await fetch(`/api/characters?id=${encodeURIComponent(id)}`, { method: 'DELETE' });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message ?? `HTTP ${res.status}`);
      setItems((prev) => prev.filter((x) => x.id !== id));
      setDeletingId(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  function openEdit(item: CharacterItem) {
    setEditingId(item.id);
    setEditName(item.name);
    setEditDesc(item.description ?? '');
    setErr(null);
  }

  async function handleSaveEdit() {
    if (!editingId) return;
    if (!editName.trim() || editName.trim().length > 100 || editDesc.trim().length > 1000) {
      setErr('名称须为 1 至 100 个字符，描述不超过 1000 个字符');
      return;
    }
    setErr(null);
    try {
      const res = await fetch(`/api/characters?id=${encodeURIComponent(editingId)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: editName.trim(), description: editDesc.trim() || null }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message ?? `HTTP ${res.status}`);
      setItems((prev) => prev.map((x) => x.id === editingId ? { ...x, name: editName.trim(), description: editDesc.trim() || null } : x));
      setEditingId(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <h1 className="text-2xl font-bold">我的人物</h1>
          <span className="badge badge-ghost">{items.length} 个</span>
        </div>
        <button
          type="button"
          className="btn btn-primary btn-sm gap-2"
          onClick={() => { setUploadOpen(true); setErr(null); }}
        >
          <Plus size={16} />上传人物
        </button>
      </div>

      {err ? <div role="alert" className="alert alert-error alert-soft text-sm">{err}</div> : null}

      {items.length === 0 ? (
        <div className="alert alert-info">
          <div>
            <h3 className="font-bold">还没有人物</h3>
            <p>上传的人物图仅自己可见，可在换装工作室中使用。</p>
          </div>
          <button type="button" className="btn btn-primary btn-sm" onClick={() => setUploadOpen(true)}>
            开始上传
          </button>
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4">
          {items.map((c) => (
            <div key={c.id} className="card card-sm bg-base-100 shadow-sm">
              <figure className="relative aspect-[3/4] w-full overflow-hidden bg-base-200">
                {c.signed_url ? (
                  // eslint-disable-next-line @next/next/no-img-element -- signed URL 动态生成
                  <img src={c.signed_url} alt={c.name} className="h-full w-full object-cover" />
                ) : (
                  <div className="flex h-full w-full items-center justify-center text-base-content/40">
                    <ImageOff size={24} />
                  </div>
                )}
              </figure>
              <div className="card-body">
                <h3 className="card-title text-sm">{c.name}</h3>
                {c.description ? <p className="text-xs text-base-content/60 line-clamp-2">{c.description}</p> : null}
                <div className="flex items-center justify-between">
                  <span className="text-xs text-base-content/40">{formatDate(c.created_at)}</span>
                  <div className="flex gap-1">
                    <button
                      type="button"
                      className="btn btn-ghost btn-xs btn-square"
                      aria-label="编辑"
                      onClick={() => openEdit(c)}
                    >
                      <Pencil size={14} />
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost btn-xs btn-square text-error"
                      aria-label="删除"
                      onClick={() => setDeletingId(c.id)}
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* 上传 Modal */}
      <dialog ref={uploadDialog} className="modal" aria-label="上传人物" onClose={() => setUploadOpen(false)}>
        <div className="modal-box max-w-lg">
          <h3 className="mb-3 text-lg font-bold">上传新人物</h3>
          {uploadOpen ? (
            <CharacterUploader
              onClose={() => setUploadOpen(false)}
              onUploaded={() => { setUploadOpen(false); refresh(); }}
            />
          ) : null}
        </div>
      </dialog>

      {/* 编辑 Modal */}
      <dialog ref={editDialog} className="modal" aria-label="编辑人物" onClose={() => setEditingId(null)}>
          <div className="modal-box max-w-sm">
            <h3 className="mb-3 text-lg font-bold">编辑人物</h3>
            <div className="space-y-3">
              <fieldset className="fieldset w-full">
                <legend className="fieldset-legend">名称</legend>
                <label htmlFor="character-edit-name">人物名称</label>
                <input
                  id="character-edit-name"
                  type="text"
                  className="input input-sm w-full"
                  value={editName}
                  onChange={(e) => setEditName(e.target.value)}
                  maxLength={100}
                />
              </fieldset>
              <fieldset className="fieldset w-full">
                <legend className="fieldset-legend">描述</legend>
                <label htmlFor="character-edit-description">人物描述</label>
                <textarea
                  id="character-edit-description"
                  className="textarea textarea-sm w-full"
                  rows={3}
                  value={editDesc}
                  onChange={(e) => setEditDesc(e.target.value)}
                  maxLength={1000}
                />
              </fieldset>
              {err ? <div role="alert" className="alert alert-error alert-soft text-sm">{err}</div> : null}
            </div>
            <div className="modal-action">
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setEditingId(null)}>取消</button>
              <button type="button" className="btn btn-sm btn-primary" onClick={handleSaveEdit}>保存</button>
            </div>
          </div>
      </dialog>

      {/* 删除确认 Modal */}
      <dialog ref={deleteDialog} className="modal" aria-label="确认删除" onClose={() => setDeletingId(null)}>
          <div className="modal-box max-w-sm">
            <h3 className="mb-2 text-lg font-bold">确认删除？</h3>
            <p className="text-sm text-base-content/70">删除后无法恢复，Storage 图片也会一并清除。</p>
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

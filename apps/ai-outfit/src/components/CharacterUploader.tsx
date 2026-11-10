'use client';

import { useRef, useState } from 'react';
import { Upload, X } from 'lucide-react';

type Props = {
  /** 上传成功后调用 */
  onUploaded?: () => void;
  /** Modal 关闭回调（可选） */
  onClose?: () => void;
};

const MAX_SIZE = 8 * 1024 * 1024;
const ACCEPTED = ['image/jpeg', 'image/png', 'image/webp'];

export function CharacterUploader({ onUploaded, onClose }: Props) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragOver, setDragOver] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [err, setErr] = useState<string | null>(null);

  function validateAndSet(f: File) {
    setErr(null);
    if (!ACCEPTED.includes(f.type)) {
      setErr(`不支持的格式：${f.type}（仅接受 jpeg/png/webp）`);
      return false;
    }
    if (f.size > MAX_SIZE) {
      setErr(`文件太大：${(f.size / 1024 / 1024).toFixed(1)}MB（上限 8MB）`);
      return false;
    }
    setFile(f);
    setName((prev) => prev || f.name.replace(/\.[^.]+$/, ''));
    const url = URL.createObjectURL(f);
    setPreview(url);
    return true;
  }

  function handleFileList(files: FileList | null) {
    if (!files || files.length === 0) return;
    validateAndSet(files[0]);
  }

  async function handleUpload() {
    if (!file || uploading) return;
    setUploading(true);
    setProgress(5);
    setErr(null);

    const form = new FormData();
    form.append('file', file);
    if (name.trim()) form.append('name', name.trim());
    if (description.trim()) form.append('description', description.trim());

    try {
      // 用 XMLHttpRequest 以获取 progress（fetch 不支持 upload progress）
      await new Promise<void>((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        xhr.open('POST', '/api/characters');
        xhr.upload.onprogress = (e) => {
          if (e.lengthComputable) {
            setProgress(Math.round((e.loaded / e.total) * 95));
          }
        };
        xhr.onload = () => {
          if (xhr.status >= 200 && xhr.status < 300) {
            setProgress(100);
            resolve();
          } else {
            let msg = `HTTP ${xhr.status}`;
            try {
              const j = JSON.parse(xhr.responseText);
              if (j.message) msg = j.message;
            } catch { /* ignore */ }
            reject(new Error(msg));
          }
        };
        xhr.onerror = () => reject(new Error('网络错误'));
        xhr.send(form);
      });
      // 清空状态
      setFile(null);
      setPreview(null);
      setName('');
      setDescription('');
      setProgress(0);
      onUploaded?.();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setUploading(false);
    }
  }

  return (
    <div className="space-y-4">
      <div
        className={`relative rounded-box border-2 border-dashed p-6 text-center transition-colors ${
          dragOver ? 'border-primary bg-primary/10' : 'border-base-300 hover:border-primary/50'
        }`}
        onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => { e.preventDefault(); setDragOver(false); handleFileList(e.dataTransfer.files); }}
      >
        <input
          ref={inputRef}
          type="file"
          accept="image/jpeg,image/png,image/webp"
          className="hidden"
          onChange={(e) => handleFileList(e.target.files)}
        />
        {preview ? (
          <div className="flex flex-col items-center gap-2">
            {/* eslint-disable-next-line @next/next/no-img-element -- blob preview */}
            <img src={preview} alt="预览" className="max-h-48 rounded object-contain" />
            <div className="flex items-center gap-2 text-xs text-base-content/60">
              <span>{file?.name}</span>
              <span>{file ? `${(file.size / 1024).toFixed(1)} KB` : ''}</span>
              <button
                type="button"
                className="btn btn-ghost btn-xs btn-circle"
                onClick={() => { setFile(null); setPreview(null); }}
                aria-label="移除"
              >
                <X size={14} />
              </button>
            </div>
          </div>
        ) : (
          <button
            type="button"
            className="flex w-full flex-col items-center gap-2"
            onClick={() => inputRef.current?.click()}
          >
            <Upload size={32} className="text-base-content/40" />
            <p className="text-sm font-medium">拖拽图片到这里，或点击选择</p>
            <p className="text-xs text-base-content/50">支持 JPG / PNG / WebP，≤ 8MB</p>
          </button>
        )}
      </div>

      {err ? <div role="alert" className="alert alert-error alert-soft text-sm">{err}</div> : null}

      {preview ? (
        <div className="space-y-2">
          <fieldset className="fieldset w-full">
            <legend className="fieldset-legend">名称</legend>
            <input
              type="text"
              className="input input-sm w-full"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="人物名称（默认用文件名）"
              maxLength={64}
            />
          </fieldset>
          <fieldset className="fieldset w-full">
            <legend className="fieldset-legend">描述（可选）</legend>
            <textarea
              className="textarea textarea-sm w-full"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="简单描述一下这个人物"
              rows={2}
              maxLength={500}
            />
          </fieldset>

          {uploading ? (
            <div className="w-full">
              <progress className="progress progress-primary w-full" value={progress} max={100} />
              <p className="mt-1 text-center text-xs text-base-content/60">上传中 {progress}%</p>
            </div>
          ) : (
            <div className="flex gap-2">
              {onClose ? (
                <button type="button" className="btn btn-sm btn-ghost" onClick={onClose}>取消</button>
              ) : null}
              <button
                type="button"
                className="btn btn-sm btn-primary flex-1"
                onClick={handleUpload}
                disabled={uploading}
              >
                {uploading ? '上传中…' : '确认上传'}
              </button>
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}

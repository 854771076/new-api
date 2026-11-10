// components/MediaCard.tsx — 通用媒体卡片（服装/姿势/人物复用）
// Server component，signedUrl 由 SSR 层预先生成。
import { ImageOff } from 'lucide-react';

type Props = {
  title: string;
  subtitle?: string | null;
  signedUrl?: string | null;
  aspectRatio?: string;    // Tailwind aspect ratio，默认 aspect-[3/4]
  onSelect?: () => void;
  selected?: boolean;
  compact?: boolean;
  variant?: 'card' | 'tile';   // tile：方形缩略图 + 底部标题覆盖，信息密度更高
};

export function MediaCard({
  title,
  subtitle,
  signedUrl,
  aspectRatio = 'aspect-[3/4]',
  onSelect,
  selected,
  compact,
  variant = 'card',
}: Props) {
  const interactive = Boolean(onSelect);

  function handleKeyDown(e: React.KeyboardEvent) {
    if (!onSelect) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onSelect();
    }
  }

  if (variant === 'tile') {
    return (
      <div
        className={[
          'group relative cursor-pointer overflow-hidden rounded-box border border-base-300 bg-base-200 transition-all duration-150',
          'aspect-square',
          interactive ? 'hover:border-primary/60' : '',
          selected ? 'ring-2 ring-primary ring-offset-2 ring-offset-base-100' : '',
        ].join(' ')}
        onClick={interactive ? onSelect : undefined}
        role={interactive ? 'button' : undefined}
        tabIndex={interactive ? 0 : undefined}
        aria-label={title}
        aria-pressed={interactive ? selected : undefined}
        onKeyDown={interactive ? handleKeyDown : undefined}
      >
        {signedUrl ? (
          // signed URL 是动态生成的，不能用 next/image（需 remotePatterns）
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={signedUrl}
            alt={title}
            loading="lazy"
            decoding="async"
            className="h-full w-full object-cover"
          />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-base-content/40">
            <ImageOff size={18} />
          </div>
        )}
        <div className="pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/70 to-transparent px-1.5 pb-1 pt-3">
          <p className="truncate text-[11px] leading-tight font-medium text-white" title={title}>{title}</p>
          {subtitle ? <p className="truncate text-[10px] leading-tight text-white/70">{subtitle}</p> : null}
        </div>
      </div>
    );
  }

  return (
    <div
      className={[
        'card border border-base-300 bg-base-100 shadow-sm transition-all duration-150',
        interactive ? 'cursor-pointer hover:shadow-md hover:-translate-y-0.5' : '',
        selected ? 'ring-2 ring-primary ring-offset-2 ring-offset-base-100' : 'ring-0',
        compact ? 'card-sm' : '',
      ].join(' ')}
      onClick={interactive ? onSelect : undefined}
      role={interactive ? 'button' : undefined}
      tabIndex={interactive ? 0 : undefined}
      onKeyDown={interactive ? handleKeyDown : undefined}
    >
      <figure className={`relative w-full overflow-hidden bg-base-200 ${aspectRatio}`}>
        {signedUrl ? (
          // signed URL 是动态生成的，不能用 next/image（需 remotePatterns）
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={signedUrl}
            alt={title}
            loading="lazy"
            className="h-full w-full object-cover"
          />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-base-content/40">
            <ImageOff size={28} />
          </div>
        )}
      </figure>
      <div className={`card-body ${compact ? 'p-2' : 'p-3'}`}>
        <h3 className={`card-title ${compact ? 'text-xs' : 'text-sm'} line-clamp-2`}>{title}</h3>
        {subtitle ? <p className="text-xs text-base-content/60 line-clamp-1">{subtitle}</p> : null}
      </div>
    </div>
  );
}

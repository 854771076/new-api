// components/MediaCard.tsx — 通用媒体卡片（服装/姿势/人物复用）
// signedUrl 由 SSR 层预先生成；仅可选卡片使用原生按钮。
import { Check, ImageOff } from 'lucide-react';

type Props = {
  title: string;
  subtitle?: string | null;
  signedUrl?: string | null;
  aspectRatio?: string;
  onSelect?: () => void;
  selected?: boolean;
  disabled?: boolean;
  compact?: boolean;
  variant?: 'card' | 'tile';
};

export function MediaCard({
  title,
  subtitle,
  signedUrl,
  aspectRatio = 'aspect-[3/4]',
  onSelect,
  selected = false,
  disabled = false,
  compact,
  variant = 'card',
}: Props) {
  const interactive = Boolean(onSelect);
  const Container = interactive ? 'button' : 'div';
  const small = compact || variant === 'tile';
  const Body = interactive ? 'span' : 'div';
  const Title = interactive ? 'span' : 'h3';

  return (
    <Container
      type={interactive ? 'button' : undefined}
      className={[
        'card card-border min-w-0 w-full overflow-hidden bg-base-100 text-left text-base-content shadow-sm',
        small ? 'card-sm' : '',
        interactive ? 'cursor-pointer motion-safe:transition-transform motion-safe:duration-150 motion-safe:enabled:hover:-translate-y-0.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:cursor-not-allowed disabled:opacity-50' : '',
        selected ? 'ring-2 ring-primary ring-offset-2 ring-offset-base-100' : '',
      ].join(' ')}
      onClick={onSelect}
      disabled={interactive ? disabled : undefined}
      aria-label={interactive ? title : undefined}
      aria-pressed={interactive ? selected : undefined}
    >
      <span className={`relative block w-full overflow-hidden bg-base-200 ${aspectRatio}`}>
        {signedUrl ? (
          // signed URL 是动态生成的，不能用 next/image（需 remotePatterns）
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={signedUrl}
            alt={interactive ? '' : title}
            loading="lazy"
            decoding="async"
            className="h-full w-full object-contain"
          />
        ) : (
          <span className="flex h-full w-full items-center justify-center text-base-content/70">
            <ImageOff size={small ? 24 : 32} aria-hidden="true" />
            <span className="sr-only">暂无图片</span>
          </span>
        )}
        {selected ? (
          <span className="badge badge-primary absolute right-2 top-2 gap-1" aria-hidden="true">
            <Check size={14} />已选
          </span>
        ) : null}
      </span>
      <Body className={`card-body w-full gap-1 ${small ? 'p-3' : 'p-4'}`}>
        <Title className="card-title line-clamp-2 text-sm leading-snug wrap-anywhere" title={title}>{title}</Title>
        {subtitle ? <span className="line-clamp-1 text-xs text-base-content/70" title={subtitle}>{subtitle}</span> : null}
      </Body>
    </Container>
  );
}

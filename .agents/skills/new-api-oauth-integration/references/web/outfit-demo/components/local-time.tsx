// components/local-time.tsx
// 历史记录的时间按浏览器本地时区显示。服务端渲染时 Node 的时区/语言和浏览器
// 未必一致，所以加 suppressHydrationWarning 避免水合警告。
'use client';

export function LocalTime({ value }: { value: string }) {
  return (
    <time dateTime={value} suppressHydrationWarning>
      {new Date(value).toLocaleString()}
    </time>
  );
}

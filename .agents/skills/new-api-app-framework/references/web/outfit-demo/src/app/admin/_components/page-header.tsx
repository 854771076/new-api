import type { ReactNode } from 'react';

export function PageHeader({ title, extra }: { title: string; extra?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
      <h1 className="m-0 text-xl font-bold">{title}</h1>
      {extra ? <div className="flex flex-wrap items-center gap-3">{extra}</div> : null}
    </div>
  );
}

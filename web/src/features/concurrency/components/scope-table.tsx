/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import { useTranslation } from "react-i18next";

import { Progress } from "@/components/ui/progress";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";

import {
  sortByUtilization,
  utilizationTone,
  type ConcurrencyScopeSnapshot,
} from "../lib/concurrency-transform";

interface ScopeTableProps {
  scopes: ConcurrencyScopeSnapshot[];
  /**
   * Window mode: rows are the window's key MEMBERSHIP rendered with live
   * values (caller merges), so only the empty state changes wording.
   */
  windowed?: boolean;
  loading?: boolean;
}

// Full variant selectors (not bare bg-*) so Tailwind's scanner sees the
// complete class and Progress's own indicator is tinted without passing a
// nested track — Progress always appends its default track after children.
const PROGRESS_TONE: Record<ReturnType<typeof utilizationTone>, string> = {
  healthy: "[&_[data-slot=progress-indicator]]:bg-primary",
  warning: "[&_[data-slot=progress-indicator]]:bg-warning",
  destructive: "[&_[data-slot=progress-indicator]]:bg-destructive",
};

// Fixed-width utilisation chip beside the bar: the number keeps one slot
// (tabular-nums, w-14) so a 9% → 100% refresh repaints digits only. Chip
// text is foreground-on-tint: the small semantic tokens miss WCAG AA 4.5:1
// on their own /10 tint in the light theme; the tint carries the tone.
const CHIP_TONE: Record<ReturnType<typeof utilizationTone>, string> = {
  healthy: "bg-success/10 text-foreground",
  warning: "bg-warning/10 text-foreground",
  destructive: "bg-destructive/10 text-foreground",
};

/**
 * Lists every active concurrency bucket, sorted by descending utilisation
 * so the hottest entries surface first. Unlimited scopes (limit=0) render
 * their utilisation as "unlimited" with no bar to make the no-cap case
 * visually distinct from capped-but-quiet buckets. The body is the card's
 * scroll area (`h-full min-h-0 overflow-y-auto`) with a sticky head, so a
 * long window list scrolls inside the card instead of the page.
 */
export function ScopeTable({ scopes, windowed, loading }: ScopeTableProps) {
  const { t } = useTranslation();
  const ordered = sortByUtilization(scopes);

  let emptyMessage: string;
  if (windowed) {
    emptyMessage = loading
      ? t("Collecting samples…")
      : t("No samples in this window.");
  } else {
    emptyMessage = t("No active concurrency buckets right now.");
  }

  return (
    <div className="h-full min-h-0 overflow-y-auto">
      {/* withContainer={false}: the scroll wrapper above must be the table's
          scrollport, otherwise the default overflow container swallows the
          sticky header and it scrolls away with the rows. */}
      <Table withContainer={false}>
        <TableHeader className="[&_th]:sticky [&_th]:top-0 [&_th]:z-10 [&_th]:bg-background">
          <TableRow>
            <TableHead>{t("Scope")}</TableHead>
            <TableHead>{t("Subject")}</TableHead>
            <TableHead>{t("Model")}</TableHead>
            <TableHead className="text-right">{t("Current / limit")}</TableHead>
            <TableHead className="min-w-[160px]">{t("Utilisation")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className="[&>tr]:h-9">
          {ordered.length === 0 ? (
            <TableRow>
              <TableCell
                colSpan={5}
                className="text-muted-foreground py-8 text-center"
              >
                {emptyMessage}
              </TableCell>
            </TableRow>
          ) : (
            ordered.map((s) => {
              const tone = utilizationTone(s.utilization, s.limit);
              const capped = s.limit > 0;
              const utilisationPct = capped
                ? Math.min(100, Math.round(s.utilization * 100))
                : 0;
              let subjectLabel: string;
              if (s.kind === "user") {
                subjectLabel = t("User #{{id}}", { id: s.subjectId });
              } else if (s.kind === "channel") {
                subjectLabel = t("Channel #{{id}}", { id: s.subjectId });
              } else {
                subjectLabel = t("Key #{{id}}", { id: s.subjectId });
              }
              return (
                <TableRow key={s.scope}>
                  <TableCell>
                    <span className="font-mono text-xs">{s.scope}</span>
                  </TableCell>
                  <TableCell>{subjectLabel}</TableCell>
                  <TableCell>
                    <span className="font-mono text-xs">{s.model}</span>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {s.current}
                    {" / "}
                    {capped ? s.limit : "∞"}
                  </TableCell>
                  <TableCell>
                    {capped ? (
                      <span className="flex items-center gap-2">
                        <Progress
                          value={utilisationPct}
                          aria-label={`${utilisationPct}%`}
                          className={cn("min-w-0 flex-1", PROGRESS_TONE[tone])}
                        />
                        {/* True utilisation, not the clamped bar fill: an
                            over-cap bucket reads 125% next to a full bar. */}
                        <span
                          className={cn(
                            "w-14 shrink-0 rounded-md px-1 py-0.5 text-center text-xs font-semibold tabular-nums",
                            CHIP_TONE[tone],
                          )}
                        >
                          {Math.round(s.utilization * 100)}%
                        </span>
                      </span>
                    ) : (
                      <span className="text-muted-foreground text-xs">
                        {t("unlimited")}
                      </span>
                    )}
                  </TableCell>
                </TableRow>
              );
            })
          )}
        </TableBody>
      </Table>
    </div>
  );
}

/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package concurrency

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// HistoryRecorder appends periodic concurrency load samples to a daily
// JSON-lines file so operators can look back at load fluctuations AFTER the
// page's 5-minute in-memory window is gone. Deliberately best-effort: the
// data is diagnostic only, so every failure (unwritable dir, snapshot error)
// degrades to a throttled log line and the next tick — recording must never
// disturb the gateway. Retention is by daily file age (HistoryRetentionDays);
// expired files are purged at startup and again whenever the date rolls.
const (
	historySampleInterval = 10 * time.Second
	HistoryRetentionDays  = 7
	historyFilePrefix     = "concurrency-"
	historyFileExt        = ".log"
)

// historyScope is one bucket row inside a sample — the raw limiter view
// (scope identity + in-flight count); configured caps are request-time
// state in the controller layer and are intentionally not re-derived here.
type historyScope struct {
	Scope   string `json:"scope"`
	Kind    string `json:"kind"`
	Current int    `json:"current"`
}

// historySample is one JSONL record: ts (unix millis), the unique in-flight
// count (summed over the user axis — exactly what the admin page's KPI
// shows), the monotonic 429 counter, and every active bucket.
type historySample struct {
	TS         int64          `json:"ts"`
	InFlight   int            `json:"inFlight"`
	Rejections int64          `json:"rejections"`
	Scopes     []historyScope `json:"scopes"`
}

// HistoryRecorder owns the sampling goroutine. Create with NewHistoryRecorder,
// Start once, Stop once (idempotent stop is not required — main shuts down
// exactly once).
type HistoryRecorder struct {
	limiter  Limiter
	dir      string
	interval time.Duration

	stopCh    chan struct{}
	doneCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once

	// lastPurgeDay guards the purge so it runs at startup and on each
	// date rollover instead of on every tick. Only touched by the
	// recording goroutine.
	lastPurgeDay string
	errLogged    bool
}

// NewHistoryRecorder builds a recorder writing into dir (typically
// *common.LogDir). interval is a parameter purely so tests can run fast;
// production wiring passes historySampleInterval via Start's default when
// zero.
func NewHistoryRecorder(l Limiter, dir string, interval time.Duration) *HistoryRecorder {
	if interval <= 0 {
		interval = historySampleInterval
	}
	return &HistoryRecorder{
		limiter:  l,
		dir:      dir,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the sampling goroutine. It purges expired files once, then
// samples on a fixed ticker until Stop.
func (r *HistoryRecorder) Start() {
	r.startOnce.Do(func() {
		go func() {
			defer close(r.doneCh)
			r.purgeExpired()
			ticker := time.NewTicker(r.interval)
			defer ticker.Stop()
			for {
				select {
				case <-r.stopCh:
					return
				case <-ticker.C:
					r.sample()
				}
			}
		}()
	})
}

// Stop halts the goroutine and waits for it to exit. Safe on a nil receiver
// so callers do not need a presence check.
func (r *HistoryRecorder) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	<-r.doneCh
}

// sample captures one snapshot and appends it as a single JSONL line.
func (r *HistoryRecorder) sample() {
	snap, err := r.limiter.Snapshot(context.Background())
	if err != nil {
		r.logErr("concurrency history: snapshot failed: " + err.Error())
		return
	}

	line := historySample{
		TS:         time.Now().UnixMilli(),
		Rejections: RejectionCount(),
		Scopes:     make([]historyScope, 0, len(snap)),
	}
	for _, s := range snap {
		if s.Kind == "user" {
			// Unique in-flight count: one user bucket per request.
			line.InFlight += s.Current
		}
		line.Scopes = append(line.Scopes, historyScope{
			Scope:   s.Scope.String(),
			Kind:    s.Kind,
			Current: s.Current,
		})
	}

	data, err := common.Marshal(line)
	if err != nil {
		r.logErr("concurrency history: marshal failed: " + err.Error())
		return
	}
	if err := r.appendLine(data); err != nil {
		r.logErr("concurrency history: append failed: " + err.Error())
		return
	}
	r.errLogged = false
}

// appendLine writes data plus a newline to today's daily file, creating the
// log directory if needed. The filename is recomputed every tick so the file
// rolls over at midnight without a separate rotation step.
func (r *HistoryRecorder) appendLine(data []byte) error {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(r.dir, historyFilePrefix+time.Now().Format("2006-01-02")+historyFileExt)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// purgeExpired removes daily files older than HistoryRetentionDays. Called at
// startup and again whenever the local date changes (so long-running nodes
// still age files out). Files with unparseable dates are left alone — they
// did not come from this recorder's naming scheme.
func (r *HistoryRecorder) purgeExpired() {
	today := time.Now().Format("2006-01-02")
	if today == r.lastPurgeDay {
		return
	}
	r.lastPurgeDay = today

	pattern := filepath.Join(r.dir, historyFilePrefix+"*"+historyFileExt)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -HistoryRetentionDays)
	for _, path := range matches {
		name := filepath.Base(path)
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, historyFilePrefix), historyFileExt)
		fileDate, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil {
			continue
		}
		if fileDate.Before(cutoff) {
			// Removal is best-effort; a locked file retries on the next rollover.
			_ = os.Remove(path)
		}
	}
}

// logErr reports a recording failure once per outage (not once per tick) so
// an unwritable log dir cannot flood the system log every interval.
func (r *HistoryRecorder) logErr(msg string) {
	if r.errLogged {
		return
	}
	r.errLogged = true
	common.SysError(msg)
}

// HistoryPoint is one chartable history sample — the JSONL record with the
// per-bucket scope detail stripped, which is all the admin chart needs and
// keeps a 7-day response compact. JSON keys mirror the frontend's
// ConcurrencyTrendPoint so the page can feed points straight into its chart.
type HistoryPoint struct {
	TS         int64 `json:"t"`
	InFlight   int   `json:"inFlight"`
	Rejections int64 `json:"rejections"`
}

// maxHistoryPoints caps one history response. A full 7 days at the 10s
// sample interval is ~60k raw lines — far past what a chart can render — so
// denser windows are bucketed down to this many points while preserving the
// per-bucket in-flight PEAK (load monitoring must not average away spikes).
const maxHistoryPoints = 720

// HistoryResult is one history-window read: the chartable point series plus
// per-bucket aggregate rows (the admin list view for the SAME window).
type HistoryResult struct {
	Points []HistoryPoint
	// ScopeStats carries one row per scope seen in the window with the peak
	// in-flight it reached (Current = peak; Limit left 0 for the controller
	// layer to enrich through its configured-cap lookup, exactly like the
	// live snapshot). Without this the bucket list can only ever show "now".
	ScopeStats []ScopeSnapshot
}

// ReadHistory returns the recorded samples whose timestamps fall inside
// [start, end], read from the daily files covering that range (missing files
// are simply absent days). Points are chronological and capped at
// maxHistoryPoints via peak-preserving bucket downsampling; ScopeStats
// aggregates each bucket's peak over the window (unbounded row count — a
// gateway's scope cardinality is small).
// Best-effort by design: an unreadable file skips that day rather than
// failing the query — the data is diagnostic, and the caller renders
// whatever exists.
func ReadHistory(dir string, start, end time.Time) HistoryResult {
	if !start.Before(end) {
		return HistoryResult{}
	}
	startMs, endMs := start.UnixMilli(), end.UnixMilli()

	var points []HistoryPoint
	peaks := make(map[ScopeKey]*ScopeSnapshot)
	// Iterate calendar days (local timezone, matching the file naming).
	for day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local); !day.After(end); day = day.AddDate(0, 0, 1) {
		path := filepath.Join(dir, historyFilePrefix+day.Format("2006-01-02")+historyFileExt)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // day never recorded (or never existed)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if len(line) == 0 {
				continue
			}
			var s historySample
			if err := common.Unmarshal([]byte(line), &s); err != nil {
				continue // tolerate a torn tail line
			}
			if s.TS < startMs || s.TS > endMs {
				continue
			}
			points = append(points, HistoryPoint{
				TS:         s.TS,
				InFlight:   s.InFlight,
				Rejections: s.Rejections,
			})
			for _, b := range s.Scopes {
				key := ScopeKey(b.Scope)
				if row, ok := peaks[key]; ok {
					if b.Current > row.Current {
						row.Current = b.Current
					}
					continue
				}
				peaks[key] = &ScopeSnapshot{
					Scope:     key,
					Kind:      b.Kind,
					SubjectID: key.SubjectID(),
					Model:     key.Model(),
					Current:   b.Current,
				}
			}
		}
	}

	stats := make([]ScopeSnapshot, 0, len(peaks))
	for _, row := range peaks {
		stats = append(stats, *row)
	}
	return HistoryResult{
		Points:     downsampleHistory(points, maxHistoryPoints),
		ScopeStats: stats,
	}
}

// downsampleHistory buckets points into at most maxPoints contiguous groups. Each
// bucket collapses to: the peak in-flight seen in the bucket, the bucket's
// LAST rejections value (the counter is monotonic, so last = end-of-bucket
// truth) and its last timestamp (keeps the x-axis chronological).
func downsampleHistory(points []HistoryPoint, maxPoints int) []HistoryPoint {
	if len(points) <= maxPoints || maxPoints <= 0 {
		return points
	}
	n := len(points)
	out := make([]HistoryPoint, 0, maxPoints)
	for b := 0; b < maxPoints; b++ {
		lo := b * n / maxPoints
		hi := (b + 1) * n / maxPoints
		if lo >= hi {
			continue
		}
		peak := points[lo]
		for _, p := range points[lo+1 : hi] {
			if p.InFlight > peak.InFlight {
				peak = p
			}
		}
		last := points[hi-1]
		out = append(out, HistoryPoint{
			TS:         last.TS,
			InFlight:   peak.InFlight,
			Rejections: last.Rejections,
		})
	}
	return out
}

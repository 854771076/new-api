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
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHistoryRecorderSamplesAndPurges pins the load-history contract: a
// running recorder appends parseable JSONL samples with the unique in-flight
// count and the rejection counter, and startup purges daily files older than
// the retention window while keeping younger ones. Data is diagnostic only,
// so the assertions cover shape and retention, not exact tick timing.
func TestHistoryRecorderSamplesAndPurges(t *testing.T) {
	dir := t.TempDir()

	// Seed one occupied user bucket (kind=user drives the inFlight sum)
	// plus one key bucket that must be logged but not double-counted.
	lim := NewMemoryLimiter()
	_, err := lim.Acquire(context.Background(), ScopeKey("user:7:*"), 0)
	require.NoError(t, err)
	_, err = lim.Acquire(context.Background(), ScopeKey("key:3:*"), 0)
	require.NoError(t, err)

	// Retention fixtures: one past the 7-day window (purged), one recent
	// (kept). Same naming scheme the recorder writes.
	expiredName := historyFilePrefix + time.Now().AddDate(0, 0, -HistoryRetentionDays-1).Format("2006-01-02") + historyFileExt
	recentName := historyFilePrefix + time.Now().AddDate(0, 0, -1).Format("2006-01-02") + historyFileExt
	require.NoError(t, os.WriteFile(filepath.Join(dir, expiredName), []byte("stale\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, recentName), []byte("yesterday\n"), 0o644))

	rejectionsBefore := RejectionCount()
	rec := NewHistoryRecorder(lim, dir, 20*time.Millisecond)
	rec.Start()
	t.Cleanup(rec.Stop)

	todayName := historyFilePrefix + time.Now().Format("2006-01-02") + historyFileExt
	todayPath := filepath.Join(dir, todayName)

	// Wait for at least two samples so the delta shape is observable.
	var lines []string
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(todayPath)
		if err != nil {
			return false
		}
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		return len(lines) >= 2
	}, 5*time.Second, 20*time.Millisecond, "the recorder must append samples to today's file")

	var first historySample
	require.NoError(t, common.Unmarshal([]byte(lines[0]), &first), "each line must be valid JSON: %s", lines[0])
	assert.Equal(t, 1, first.InFlight,
		"the user axis sums to the unique in-flight count; the key bucket must not double it")
	assert.GreaterOrEqual(t, first.Rejections, rejectionsBefore,
		"samples carry the monotonic rejection counter")
	assert.Positive(t, first.TS, "samples carry a unix-millis timestamp")

	kinds := map[string]string{}
	for _, s := range first.Scopes {
		kinds[s.Scope] = s.Kind
	}
	require.Equal(t, "user", kinds["user:7:*"], "both active buckets are logged with their kind")
	require.Equal(t, "key", kinds["key:3:*"])

	// Retention: expired file gone, recent file untouched.
	_, err = os.Stat(filepath.Join(dir, expiredName))
	assert.True(t, os.IsNotExist(err), "files past the 7-day window must be purged")
	_, err = os.Stat(filepath.Join(dir, recentName))
	assert.NoError(t, err, "files inside the window must survive")

	rec.Stop() // idempotent with the cleanup Stop; must not hang
}

// TestHistoryRecorderSurvivesUnwritableDir proves recording is best-effort:
// an impossible directory yields dropped samples and a healthy Stop, never a
// panic or a wedged goroutine.
func TestHistoryRecorderSurvivesUnwritableDir(t *testing.T) {
	// A path under an existing FILE cannot be created — MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	lim := NewMemoryLimiter()
	rec := NewHistoryRecorder(lim, filepath.Join(blocker, "logs"), 10*time.Millisecond)
	rec.Start()
	time.Sleep(50 * time.Millisecond) // several failing ticks must not crash
	rec.Stop()
}

// TestReadHistoryFiltersAndDownsamples pins the read contract the admin
// time-window picker depends on: only samples inside [start, end] come back,
// chronological, missing days are tolerated, and a dense window collapses to
// maxHistoryPoints while keeping the in-flight PEAK and the bucket-end
// rejection counter.
func TestReadHistoryFiltersAndDownsamples(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	day := func(d time.Time) string {
		return historyFilePrefix + d.Format("2006-01-02") + historyFileExt
	}
	writeLines := func(path string, samples []historySample) {
		var buf []byte
		for _, s := range samples {
			data, err := common.Marshal(s)
			require.NoError(t, err)
			buf = append(buf, data...)
			buf = append(buf, '\n')
		}
		require.NoError(t, os.WriteFile(path, buf, 0o644))
	}

	// Today: 800 points at 10s spacing, all inside the requested window,
	// with one unmistakable spike at index 400 (inFlight 42).
	base := now.Add(-3 * time.Hour).Truncate(time.Second)
	dense := make([]historySample, 0, 800)
	for i := range 800 {
		inFlight := i % 5
		if i == 400 {
			inFlight = 42
		}
		dense = append(dense, historySample{
			TS:         base.Add(time.Duration(i) * 10 * time.Second).UnixMilli(),
			InFlight:   inFlight,
			Rejections: int64(i),
			Scopes: []historyScope{
				{Scope: "key:1:*", Kind: "key", Current: inFlight},
				{Scope: "user:7:*", Kind: "user", Current: 2},
			},
		})
	}
	writeLines(filepath.Join(dir, day(now)), dense)

	// Yesterday: three points entirely BEFORE the window — must not leak in.
	writeLines(filepath.Join(dir, day(now.AddDate(0, 0, -1))), []historySample{
		{TS: base.Add(-26 * time.Hour).UnixMilli(), InFlight: 9, Rejections: 1},
		{TS: base.Add(-25 * time.Hour).UnixMilli(), InFlight: 9, Rejections: 2},
		{TS: base.Add(-24 * time.Hour).UnixMilli(), InFlight: 9, Rejections: 3},
	})

	start := base.Add(-time.Minute)
	end := now
	result := ReadHistory(dir, start, end)
	points := result.Points

	require.LessOrEqual(t, len(points), maxHistoryPoints, "dense windows must downsample")
	require.Positive(t, len(points), "in-range samples must be returned")

	peak := 0
	for i, p := range points {
		require.GreaterOrEqual(t, p.TS, start.UnixMilli(), "no point may precede the window")
		require.LessOrEqual(t, p.TS, end.UnixMilli(), "no point may follow the window")
		if i > 0 {
			require.GreaterOrEqual(t, p.TS, points[i-1].TS, "points stay chronological")
		}
		if p.InFlight > peak {
			peak = p.InFlight
		}
	}
	require.Equal(t, 42, peak, "downsampling must preserve the in-flight spike")
	require.EqualValues(t, 799, points[len(points)-1].Rejections,
		"the newest point keeps its exact rejection counter (no clamping)")

	// ScopeStats aggregate the window's per-bucket PEAKS with parsed ids.
	byScope := map[string]ScopeSnapshot{}
	for _, row := range result.ScopeStats {
		byScope[row.Scope.String()] = row
	}
	keyRow, ok := byScope["key:1:*"]
	require.True(t, ok, "the key bucket must appear in the window stats")
	assert.Equal(t, 42, keyRow.Current, "stats carry the bucket's PEAK, not the last sample")
	assert.Equal(t, "key", keyRow.Kind)
	assert.Equal(t, 1, keyRow.SubjectID)
	userRow, ok := byScope["user:7:*"]
	require.True(t, ok, "the user bucket must appear in the window stats")
	assert.Equal(t, 2, userRow.Current)
	assert.Equal(t, "user", userRow.Kind)

	// A missing directory reads as empty history, not an error.
	assert.Empty(t, ReadHistory(filepath.Join(dir, "nope"), start, end).Points)
	// An inverted window reads as empty too.
	assert.Empty(t, ReadHistory(dir, end, start).Points)
}

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

import "sync/atomic"

// rejectionCount counts client-visible concurrency 429s since process start.
// Both enforcement surfaces increment it — the token/user middleware gate
// (middleware/concurrency.go rejectConcurrency) and the all-channels-saturated
// terminal render (controller/relay.go renderRelayTerminalError) — so the
// admin snapshot can surface "how much load was actually shed" without
// scraping logs. Monotonic per process: the frontend derives windowed deltas
// from consecutive snapshot reads, so a restart simply restarts the series.
var rejectionCount atomic.Int64

// AddRejection records one client-visible concurrency 429 response.
func AddRejection() {
	rejectionCount.Add(1)
}

// RejectionCount returns the total concurrency 429s since process start.
func RejectionCount() int64 {
	return rejectionCount.Load()
}

package concurrency

import "time"

// realWallClockMillis is the production binding for wallClockMillis.
func realWallClockMillis() int64 {
	return time.Now().UnixMilli()
}

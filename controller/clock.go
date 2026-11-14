package controller

import "time"

// timeNowUnixMilli is the production binding for nowUnixMilli, kept in its
// own file so test files can swap it without touching the controller body.
func timeNowUnixMilli() int64 {
	return time.Now().UnixMilli()
}

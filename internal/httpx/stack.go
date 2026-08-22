package httpx

import "runtime"

// stackTrace renders the current goroutine's stack for panic logging.
func stackTrace() string {
	buf := make([]byte, 8<<10)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

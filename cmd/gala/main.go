package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // optional pprof endpoints, gated on GALA_PPROF
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"time"

	"martianoff/gala/cmd/gala/commands"
)

// Optional performance-debugging knobs. All are off unless their env var is
// set, so production builds carry no overhead beyond a few env reads at
// startup. See docs/PERFORMANCE_DEBUGGING.MD for usage.
//
//	GALA_PPROF=host:port              start net/http/pprof at the given addr.
//	GALA_HEAP_DUMP_DIR=<dir>          write `heap_<N>MB.pb` under <dir> each
//	                                   time HeapAlloc crosses a new band.
//	GALA_HEAP_DUMP_BAND_MB=<int>      band size in MB (default 500).
func main() {
	if addr := os.Getenv("GALA_PPROF"); addr != "" {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
		go func() {
			log.Println("gala: pprof listening on", addr)
			log.Println(http.ListenAndServe(addr, nil))
		}()
	}
	if dir := os.Getenv("GALA_HEAP_DUMP_DIR"); dir != "" {
		band := uint64(500)
		if v := os.Getenv("GALA_HEAP_DUMP_BAND_MB"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
				band = n
			}
		}
		go heapWatchdog(dir, band)
	}
	commands.Execute()
}

// heapWatchdog writes a heap profile each time HeapAlloc crosses a new
// `bandMB`-megabyte band (500MB, 1GB, 1.5GB ...). Snapshots are named
// `heap_<MB>MB.pb` and analyzable via `go tool pprof <gala.exe> <file>`.
// Cheap when the band is large enough that crossings are rare; the
// runtime.GC before each write costs a full GC cycle.
func heapWatchdog(dir string, bandMB uint64) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Println("gala: heap watchdog mkdir:", err)
		return
	}
	thresh := bandMB * 1024 * 1024
	step := uint64(0)
	var ms runtime.MemStats
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		runtime.ReadMemStats(&ms)
		curBand := ms.HeapAlloc / thresh
		if curBand <= step {
			continue
		}
		step = curBand
		path := fmt.Sprintf("%s/heap_%dMB.pb", dir, curBand*bandMB)
		f, err := os.Create(path)
		if err != nil {
			log.Println("gala: heap watchdog create:", err)
			continue
		}
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Println("gala: heap watchdog write:", err)
		}
		f.Close()
		log.Printf("gala: heap snapshot %s (heap_alloc=%d sys=%d)", path, ms.HeapAlloc, ms.Sys)
	}
}

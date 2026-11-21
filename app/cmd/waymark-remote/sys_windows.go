package main

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
	user32                      = windows.NewLazySystemDLL("user32.dll")
	procEnumDisplayMonitors     = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW         = user32.NewProc("GetMonitorInfoW")
	procFindWindowW             = user32.NewProc("FindWindowW")
	procSetWindowPos            = user32.NewProc("SetWindowPos")
)

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

var (
	awakeOnce sync.Once
	awakeCh   = make(chan bool, 1)
)

// keepAwake stops sleep and screen-off while a show runs. The flag belongs to
// a thread, so one locked goroutine holds it.
func keepAwake(on bool) {
	awakeOnce.Do(func() {
		go func() {
			runtime.LockOSThread()
			cur := false
			for v := range awakeCh {
				if v == cur {
					continue
				}
				cur = v
				flags := uintptr(esContinuous)
				if v {
					flags |= esSystemRequired | esDisplayRequired
				}
				procSetThreadExecutionState.Call(flags)
			}
		}()
	})
	select {
	case awakeCh <- on:
	default:
		<-awakeCh
		awakeCh <- on
	}
}

type rect struct{ left, top, right, bottom int32 }

type monitorInfo struct {
	size    uint32
	monitor rect
	work    rect
	flags   uint32
}

const monitorinfoPrimary = 1

// moveToProjector moves the window onto the first screen that is not the
// main one, before it goes full screen. With one screen it stays put.
func moveToProjector(title string) {
	var target *rect
	cb := syscall.NewCallback(func(h, _ uintptr, _ uintptr, _ uintptr) uintptr {
		mi := monitorInfo{size: uint32(unsafe.Sizeof(monitorInfo{}))}
		procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
		if mi.flags&monitorinfoPrimary == 0 {
			r := mi.monitor
			target = &r
			return 0
		}
		return 1
	})
	procEnumDisplayMonitors.Call(0, 0, cb, 0)
	if target == nil {
		return
	}
	t, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(t)))
	if hwnd == 0 {
		return
	}
	const swpNoZOrder, swpNoActivate = 0x0004, 0x0010
	procSetWindowPos.Call(hwnd, 0, uintptr(target.left+40), uintptr(target.top+40),
		uintptr(target.right-target.left-80), uintptr(target.bottom-target.top-80), swpNoZOrder|swpNoActivate)
}

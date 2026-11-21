package pres

import (
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/koulaxizis/waymark/app/internal/proto"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procSendInput                = user32.NewProc("SendInput")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	shell32                      = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW            = shell32.NewProc("ShellExecuteW")
)

// keyboardInput is INPUT with a KEYBDINPUT, laid out for 64-bit Windows.
type keyboardInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
	_     [8]byte
}

const (
	inputKeyboard  = 1
	keyEventFKeyUp = 0x0002
	vkMenu         = 0x12
)

var vk = map[Key]uint16{KeyRight: 0x27, KeyLeft: 0x25, KeyB: 0x42, KeyEsc: 0x1B, KeyF5: 0x74}

func press(code uint16, up bool) keyboardInput {
	in := keyboardInput{typ: inputKeyboard, vk: code}
	if up {
		in.flags = keyEventFKeyUp
	}
	return in
}

func sendInputs(ins []keyboardInput) error {
	n, _, err := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
	if int(n) != len(ins) {
		return Errf(proto.ErrPermission, "Windows refused the keystroke: "+err.Error())
	}
	return nil
}

// focus brings a visible window of pid to the front. Windows only lets a
// background process do that right after a key event, hence the Alt tap.
func focus(pid int) {
	var target uintptr
	cb := syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		var p uint32
		procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&p)))
		if int(p) == pid {
			if v, _, _ := procIsWindowVisible.Call(h); v != 0 {
				target = h
				return 0
			}
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	if target == 0 {
		return
	}
	sendInputs([]keyboardInput{press(vkMenu, false), press(vkMenu, true)})
	procSetForegroundWindow.Call(target)
	time.Sleep(30 * time.Millisecond)
}

func sendKey(pid int, k Key) error {
	if pid > 0 {
		focus(pid)
	}
	c := vk[k]
	return sendInputs([]keyboardInput{press(c, false), press(c, true)})
}

func openDefault(path string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
	if r <= 32 {
		return syscall.Errno(r)
	}
	return nil
}

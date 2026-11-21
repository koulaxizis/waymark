package files

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSHFileOperationW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")

type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

const (
	foDelete          = 3
	fofSilent         = 0x0004
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040
	fofNoErrorUI      = 0x0400
	fofNoConfirmMkdir = 0x0200
)

// moveToTrash sends p to the Recycle Bin of the current user (no admin).
func moveToTrash(p string) error {
	u, err := syscall.UTF16FromString(p)
	if err != nil {
		return err
	}
	u = append(u, 0) // the list is double-NUL terminated
	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &u[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI | fofNoConfirmMkdir,
	}
	r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return errors.New("recycle bin refused the file")
	}
	if op.fAnyOperationsAborted != 0 {
		return errors.New("recycle bin operation aborted")
	}
	return nil
}

func hideDir(p string) {
	if u, err := syscall.UTF16PtrFromString(p); err == nil {
		windows.SetFileAttributes(u, windows.FILE_ATTRIBUTE_HIDDEN)
	}
}

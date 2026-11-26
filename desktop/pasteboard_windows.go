package main

import (
	"errors"
	"fmt"
	"runtime"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/windows"

	"github.com/iyazerski/clipsync/internal/protocol"
)

const unicodeText = 13 // CF_UNICODETEXT

type winPasteboard struct {
	open, close, empty, count, available, get, set, register *windows.LazyProc
	alloc, free, unlock, size, createWindow, destroyWindow   *windows.LazyProc
	lock                                                     func(uintptr) unsafe.Pointer
	private, cloud                                           uint32
}

func newPasteboard() (*winPasteboard, error) {
	user := windows.NewLazySystemDLL("user32.dll")
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	b := &winPasteboard{
		open:          user.NewProc("OpenClipboard"),
		close:         user.NewProc("CloseClipboard"),
		empty:         user.NewProc("EmptyClipboard"),
		count:         user.NewProc("GetClipboardSequenceNumber"),
		available:     user.NewProc("IsClipboardFormatAvailable"),
		get:           user.NewProc("GetClipboardData"),
		set:           user.NewProc("SetClipboardData"),
		register:      user.NewProc("RegisterClipboardFormatW"),
		alloc:         kernel.NewProc("GlobalAlloc"),
		free:          kernel.NewProc("GlobalFree"),
		unlock:        kernel.NewProc("GlobalUnlock"),
		size:          kernel.NewProc("GlobalSize"),
		createWindow:  user.NewProc("CreateWindowExW"),
		destroyWindow: user.NewProc("DestroyWindow"),
	}
	lock := kernel.NewProc("GlobalLock")
	if err := lock.Find(); err != nil {
		return nil, err
	}
	purego.RegisterFunc(&b.lock, lock.Addr())
	for name, format := range map[string]*uint32{
		"ExcludeClipboardContentFromMonitorProcessing": &b.private,
		"CanUploadToCloudClipboard":                    &b.cloud,
	} {
		wide, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		id, _, err := b.register.Call(uintptr(unsafe.Pointer(wide)))
		if id == 0 {
			return nil, fmt.Errorf("registering clipboard format %s: %w", name, err)
		}
		*format = uint32(id)
	}
	return b, nil
}

func (b *winPasteboard) ChangeCount() uint64 {
	n, _, _ := b.count.Call()
	return uint64(n)
}

func (b *winPasteboard) withClipboard(writing bool, fn func() error) error {
	// Win32's open clipboard belongs to the calling thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var owner uintptr
	if writing {
		// EmptyClipboard needs a window owner for the subsequent SetClipboardData.
		class := [...]uint16{'S', 'T', 'A', 'T', 'I', 'C', 0}
		const messageOnly = ^uintptr(2) // HWND_MESSAGE
		h, _, err := b.createWindow.Call(0, uintptr(unsafe.Pointer(&class[0])), 0, 0, 0, 0, 0, 0, messageOnly, 0, 0, 0)
		if h == 0 {
			return fmt.Errorf("creating a clipboard owner: %w", err)
		}
		owner = h
		defer b.destroyWindow.Call(owner)
	}
	var last error
	for range 200 {
		ok, _, err := b.open.Call(owner)
		if ok != 0 {
			defer b.close.Call()
			return fn()
		}
		last = err
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("opening the clipboard: %w", last)
}

func (b *winPasteboard) Read() (string, error) {
	var text string
	err := b.withClipboard(false, func() error {
		if private, _, _ := b.available.Call(uintptr(b.private)); private != 0 {
			return nil
		}
		if h, _, _ := b.get.Call(uintptr(b.cloud)); h != 0 {
			if size, _, _ := b.size.Call(h); size >= 4 {
				if ptr := b.lock(h); ptr != nil {
					private := *(*uint32)(ptr) == 0
					b.unlock.Call(h)
					if private {
						return nil
					}
				}
			}
		}
		if available, _, _ := b.available.Call(unicodeText); available == 0 {
			return nil
		}
		h, _, err := b.get.Call(unicodeText)
		if h == 0 {
			return fmt.Errorf("reading the clipboard: %w", err)
		}
		n, _, _ := b.size.Call(h)
		ptr := b.lock(h)
		if ptr == nil {
			return errors.New("locking the clipboard data")
		}
		defer b.unlock.Call(h)
		// Read one extra code unit to distinguish a maximum-size clip from a longer one.
		units := unsafe.Slice((*uint16)(ptr), int(min(n/2, protocol.MaxText+1)))
		text = windows.UTF16ToString(units)
		return nil
	})
	return text, err
}

func (b *winPasteboard) Write(text string) (uint64, error) {
	wide := append(utf16.Encode([]rune(text)), 0)
	var count uint64
	err := b.withClipboard(true, func() error {
		const moveable = 2 // GMEM_MOVEABLE, required by SetClipboardData.
		h, _, err := b.alloc.Call(moveable, uintptr(len(wide)*2))
		if h == 0 {
			return fmt.Errorf("allocating clipboard data: %w", err)
		}
		owned := true
		defer func() {
			if owned {
				b.free.Call(h)
			}
		}()
		ptr := b.lock(h)
		if ptr == nil {
			return errors.New("locking the clipboard data")
		}
		copy(unsafe.Slice((*uint16)(ptr), len(wide)), wide)
		b.unlock.Call(h)
		if ok, _, err := b.empty.Call(); ok == 0 {
			return fmt.Errorf("emptying the clipboard: %w", err)
		}
		if ok, _, err := b.set.Call(unicodeText, h); ok == 0 {
			return fmt.Errorf("writing the clipboard: %w", err)
		}
		owned = false
		count = b.ChangeCount()
		return nil
	})
	return count, err
}

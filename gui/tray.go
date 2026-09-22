package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

//go:embed icons/tray_offline.ico
var trayIconOfflineBytes []byte

//go:embed icons/tray_online.ico
var trayIconOnlineBytes []byte

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassExW    = user32.NewProc("RegisterClassExW")
	pCreateWindowExW     = user32.NewProc("CreateWindowExW")
	pDefWindowProcW      = user32.NewProc("DefWindowProcW")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pPostMessageW        = user32.NewProc("PostMessageW")
	pGetMessageW         = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessageW    = user32.NewProc("DispatchMessageW")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenuW         = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pLoadImageW          = user32.NewProc("LoadImageW")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type POINT struct {
	X, Y int32
}

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	TimeoutOrVersion uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type MSG struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

const (
	NIM_ADD          = 0x00000000
	NIM_MODIFY       = 0x00000001
	NIM_DELETE       = 0x00000002
	NIF_MESSAGE      = 0x00000001
	NIF_ICON         = 0x00000002
	NIF_TIP          = 0x00000004
	WM_USER          = 0x0400
	WM_TRAYICON      = WM_USER + 1
	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205
	WM_CLOSE         = 0x0010
	WM_DESTROY       = 0x0002

	IMAGE_ICON      = 1
	LR_LOADFROMFILE = 0x00000010
	LR_DEFAULTSIZE  = 0x00000040

	MF_STRING       = 0x00000000
	MF_GRAYED       = 0x00000001
	MF_DISABLED     = 0x00000002
	MF_SEPARATOR    = 0x00000800
	TPM_BOTTOMALIGN = 0x0020
	TPM_RIGHTALIGN  = 0x0008
	TPM_RETURNCMD   = 0x0100

	ID_TRAY_OPEN    = 1001
	ID_TRAY_PHONE   = 1002
	ID_TRAY_EMU     = 1003
	ID_TRAY_PROFILE = 1004
	ID_TRAY_QUIT    = 1005
)

// TrayManager manages the Windows notification area system tray icon and context menu.
type TrayManager struct {
	app          *App
	hwnd         uintptr
	nid          NOTIFYICONDATAW
	nidMu        sync.Mutex
	hIconOffline uintptr
	hIconOnline  uintptr
	currentIcon  uintptr

	ready    atomic.Bool
	stopChan chan struct{}
	stopOnce sync.Once

	lastOnline   bool
	lastPhone    string
	lastEmuCount int
	lastProfile  string
	lastLang     string
}

// NewTrayManager initializes a pure Win32 tray manager.
func NewTrayManager(app *App) *TrayManager {
	tm := &TrayManager{
		app:      app,
		stopChan: make(chan struct{}),
	}
	return tm
}

// Start launches the dedicated Win32 thread and tray message loop.
func (tm *TrayManager) Start() {
	readyChan := make(chan struct{})
	go tm.trayLoop(readyChan)
	<-readyChan
	go tm.updateLoop()
}

// Stop terminates the tray icon and exits the Win32 message loop.
func (tm *TrayManager) Stop() {
	tm.stopOnce.Do(func() {
		close(tm.stopChan)
		if tm.hwnd != 0 {
			tm.nidMu.Lock()
			pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&tm.nid)))
			tm.nidMu.Unlock()
			pPostMessageW.Call(tm.hwnd, WM_CLOSE, 0, 0)
		}
	})
}

func (tm *TrayManager) trayLoop(readyChan chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 1. Prepare temp icon files
	tempDir := filepath.Join(os.TempDir(), "gyrobridge_tray")
	_ = os.MkdirAll(tempDir, 0755)

	offPath := filepath.Join(tempDir, "tray_off.ico")
	onPath := filepath.Join(tempDir, "tray_on.ico")
	_ = os.WriteFile(offPath, trayIconOfflineBytes, 0644)
	_ = os.WriteFile(onPath, trayIconOnlineBytes, 0644)

	offPtr, _ := syscall.UTF16PtrFromString(offPath)
	onPtr, _ := syscall.UTF16PtrFromString(onPath)

	hOff, _, _ := pLoadImageW.Call(0, uintptr(unsafe.Pointer(offPtr)), IMAGE_ICON, 0, 0, LR_LOADFROMFILE|LR_DEFAULTSIZE)
	hOn, _, _ := pLoadImageW.Call(0, uintptr(unsafe.Pointer(onPtr)), IMAGE_ICON, 0, 0, LR_LOADFROMFILE|LR_DEFAULTSIZE)

	tm.hIconOffline = hOff
	tm.hIconOnline = hOn
	tm.currentIcon = hOff

	// 2. Register window class
	hInstance, _, _ := pGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString(fmt.Sprintf("GyroBridgeTray_%d", time.Now().UnixNano()))

	wndProcCallback := syscall.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
		switch msg {
		case WM_TRAYICON:
			switch lParam {
			case WM_LBUTTONUP, WM_LBUTTONDBLCLK:
				tm.app.ShowWindow()
				return 0

			case WM_RBUTTONUP:
				tm.showContextMenu(hwnd)
				return 0
			}

		case WM_CLOSE:
			pDestroyWindow.Call(hwnd)
			return 0

		case WM_DESTROY:
			pPostQuitMessage.Call(0)
			return 0
		}

		ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return ret
	})

	wcex := WNDCLASSEXW{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		LpfnWndProc:   wndProcCallback,
		HInstance:     hInstance,
		LpszClassName: className,
	}
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcex)))

	// 3. Create hidden message window
	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0,
		0, 0, hInstance, 0,
	)
	tm.hwnd = hwnd

	// 4. Register tray icon
	tm.nid = NOTIFYICONDATAW{
		CbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATAW{})),
		HWnd:             hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		UCallbackMessage: WM_TRAYICON,
		HIcon:            tm.currentIcon,
	}
	tip, _ := syscall.UTF16FromString("GyroBridge")
	copy(tm.nid.SzTip[:], tip)

	pShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&tm.nid)))
	tm.ready.Store(true)
	close(readyChan)

	// 5. Message pump
	var msg MSG
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	// 6. Cleanup
	pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&tm.nid)))
	tm.ready.Store(false)
}

func (tm *TrayManager) showContextMenu(hwnd uintptr) {
	var pt POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(hwnd)

	hMenu, _, _ := pCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer pDestroyMenu.Call(hMenu)

	isRu := tm.app.GetLang() == "ru"
	hasPhone := tm.app.hasClient.Load()
	phoneName := ""
	if v := tm.app.deviceName.Load(); v != nil {
		if s, ok := v.(string); ok && s != "" {
			phoneName = s
		}
	}
	if phoneName == "" {
		phoneName = "iPhone"
	}

	emuCount := 0
	if tm.app.dsuSrv != nil {
		emuCount = tm.app.dsuSrv.ActiveClientCount()
	}
	profileName := tm.app.getActiveProfileName()

	var openStr, phoneStr, emuStr, profStr, quitStr string

	if isRu {
		openStr = "Открыть GyroBridge"
		quitStr = "Выход"
		if hasPhone {
			phoneStr = fmt.Sprintf("📱 %s (Онлайн)", phoneName)
		} else {
			phoneStr = "📱 Телефон: Ожидание подключения"
		}
		if emuCount > 0 {
			emuStr = fmt.Sprintf("🎮 Эмуляторы: Подключен (%d)", emuCount)
		} else {
			emuStr = "🎮 Эмуляторы: Нет подключений"
		}
		profStr = fmt.Sprintf("⚡ Профиль: %s", profileName)
	} else {
		openStr = "Open GyroBridge"
		quitStr = "Quit"
		if hasPhone {
			phoneStr = fmt.Sprintf("📱 %s (Online)", phoneName)
		} else {
			phoneStr = "📱 Phone: Waiting for connection"
		}
		if emuCount > 0 {
			emuStr = fmt.Sprintf("🎮 Emulators: Connected (%d)", emuCount)
		} else {
			emuStr = "🎮 Emulators: No clients"
		}
		profStr = fmt.Sprintf("⚡ Profile: %s", profileName)
	}

	// Append Menu Items
	appendMenuItem(hMenu, MF_STRING, ID_TRAY_OPEN, openStr)
	appendMenuItem(hMenu, MF_SEPARATOR, 0, "")
	appendMenuItem(hMenu, MF_GRAYED|MF_DISABLED, ID_TRAY_PHONE, phoneStr)
	appendMenuItem(hMenu, MF_GRAYED|MF_DISABLED, ID_TRAY_EMU, emuStr)
	appendMenuItem(hMenu, MF_GRAYED|MF_DISABLED, ID_TRAY_PROFILE, profStr)
	appendMenuItem(hMenu, MF_SEPARATOR, 0, "")
	appendMenuItem(hMenu, MF_STRING, ID_TRAY_QUIT, quitStr)

	cmd, _, _ := pTrackPopupMenu.Call(
		hMenu,
		TPM_BOTTOMALIGN|TPM_RIGHTALIGN|TPM_RETURNCMD,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		hwnd,
		0,
	)

	switch cmd {
	case ID_TRAY_OPEN:
		tm.app.ShowWindow()
	case ID_TRAY_QUIT:
		tm.app.QuitApp()
	}
}

func appendMenuItem(hMenu uintptr, flags uint32, id uint32, text string) {
	if text == "" && flags&MF_SEPARATOR != 0 {
		pAppendMenuW.Call(hMenu, uintptr(flags), uintptr(id), 0)
		return
	}
	textPtr, _ := syscall.UTF16PtrFromString(text)
	pAppendMenuW.Call(hMenu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(textPtr)))
}

func (tm *TrayManager) updateLoop() {
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	tm.refresh()

	for {
		select {
		case <-tm.stopChan:
			return
		case <-ticker.C:
			tm.refresh()
		}
	}
}

// UpdateState immediately refreshes the tray icon and menu state.
func (tm *TrayManager) UpdateState() {
	tm.refresh()
}

func (tm *TrayManager) refresh() {
	if !tm.ready.Load() || tm.hwnd == 0 {
		return
	}

	hasPhone := tm.app.hasClient.Load()
	phoneName := ""
	if v := tm.app.deviceName.Load(); v != nil {
		if s, ok := v.(string); ok && s != "" {
			phoneName = s
		}
	}
	if phoneName == "" {
		phoneName = "iPhone"
	}

	emuCount := 0
	if tm.app.dsuSrv != nil {
		emuCount = tm.app.dsuSrv.ActiveClientCount()
	}

	profileName := tm.app.getActiveProfileName()
	lang := tm.app.GetLang()

	stateChanged := tm.lastOnline != hasPhone ||
		tm.lastPhone != phoneName ||
		tm.lastEmuCount != emuCount ||
		tm.lastProfile != profileName ||
		tm.lastLang != lang

	if !stateChanged {
		return
	}

	tm.lastOnline = hasPhone
	tm.lastPhone = phoneName
	tm.lastEmuCount = emuCount
	tm.lastProfile = profileName
	tm.lastLang = lang

	isRu := lang == "ru"

	// Select icon
	var iconToSet uintptr
	if hasPhone && tm.hIconOnline != 0 {
		iconToSet = tm.hIconOnline
	} else if tm.hIconOffline != 0 {
		iconToSet = tm.hIconOffline
	}

	// Select tooltip
	var tipText string
	if isRu {
		if hasPhone {
			tipText = fmt.Sprintf("GyroBridge — %s (Онлайн)", phoneName)
		} else {
			tipText = "GyroBridge — Ожидание подключения"
		}
	} else {
		if hasPhone {
			tipText = fmt.Sprintf("GyroBridge — %s (Online)", phoneName)
		} else {
			tipText = "GyroBridge — Waiting for connection"
		}
	}

	tm.nidMu.Lock()
	defer tm.nidMu.Unlock()

	tm.nid.UFlags = NIF_ICON | NIF_TIP
	if iconToSet != 0 {
		tm.nid.HIcon = iconToSet
	}
	tipUTF16, _ := syscall.UTF16FromString(tipText)
	copy(tm.nid.SzTip[:], tipUTF16)

	pShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&tm.nid)))
}

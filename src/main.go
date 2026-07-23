//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// ----------------------------- Win32 bindings -----------------------------

type HWND uintptr
type HDC uintptr
type HBITMAP uintptr
type HGDIOBJ uintptr
type HFONT uintptr
type HINSTANCE uintptr
type HICON uintptr
type HCURSOR uintptr
type HBRUSH uintptr
type HMENU uintptr
type HINTERNET uintptr

type POINT struct{ X, Y int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }
type MSG struct {
	Hwnd     HWND
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       POINT
	LPrivate uint32
}
type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     HINSTANCE
	HIcon         HICON
	HCursor       HCURSOR
	HbrBackground HBRUSH
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       HICON
}
type PAINTSTRUCT struct {
	Hdc         HDC
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}
type TRACKMOUSEEVENT struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   HWND
	DwHoverTime uint32
}
type WINHTTP_CURRENT_USER_IE_PROXY_CONFIG struct {
	AutoDetect    int32
	AutoConfigURL *uint16
	Proxy         *uint16
	ProxyBypass   *uint16
}

type backBuffer struct {
	dc   HDC
	bmp  HBITMAP
	old  HGDIOBJ
	w, h int32
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")
	winhttp  = syscall.NewLazyDLL("winhttp.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pUpdateWindow                  = user32.NewProc("UpdateWindow")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pBeginPaint                    = user32.NewProc("BeginPaint")
	pEndPaint                      = user32.NewProc("EndPaint")
	pGetClientRect                 = user32.NewProc("GetClientRect")
	pScreenToClient                = user32.NewProc("ScreenToClient")
	pInvalidateRect                = user32.NewProc("InvalidateRect")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pSetTimer                      = user32.NewProc("SetTimer")
	pKillTimer                     = user32.NewProc("KillTimer")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pSetCursor                     = user32.NewProc("SetCursor")
	pTrackMouseEvent               = user32.NewProc("TrackMouseEvent")
	pGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pGetWindowRect                 = user32.NewProc("GetWindowRect")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pGlobalFree       = kernel32.NewProc("GlobalFree")
	pMoveFileExW      = kernel32.NewProc("MoveFileExW")

	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pGetStockObject         = gdi32.NewProc("GetStockObject")
	pSetDCBrushColor        = gdi32.NewProc("SetDCBrushColor")
	pSetDCPenColor          = gdi32.NewProc("SetDCPenColor")
	pRectangle              = gdi32.NewProc("Rectangle")
	pRoundRect              = gdi32.NewProc("RoundRect")
	pEllipse                = gdi32.NewProc("Ellipse")
	pMoveToEx               = gdi32.NewProc("MoveToEx")
	pLineTo                 = gdi32.NewProc("LineTo")
	pSaveDC                 = gdi32.NewProc("SaveDC")
	pRestoreDC              = gdi32.NewProc("RestoreDC")
	pIntersectClipRect      = gdi32.NewProc("IntersectClipRect")
	pSetBkMode              = gdi32.NewProc("SetBkMode")
	pSetTextColor           = gdi32.NewProc("SetTextColor")
	pCreateFontW            = gdi32.NewProc("CreateFontW")
	pDrawTextW              = user32.NewProc("DrawTextW")

	pShellExecuteW = shell32.NewProc("ShellExecuteW")

	pWinHttpOpen                           = winhttp.NewProc("WinHttpOpen")
	pWinHttpConnect                        = winhttp.NewProc("WinHttpConnect")
	pWinHttpOpenRequest                    = winhttp.NewProc("WinHttpOpenRequest")
	pWinHttpAddRequestHeaders              = winhttp.NewProc("WinHttpAddRequestHeaders")
	pWinHttpSendRequest                    = winhttp.NewProc("WinHttpSendRequest")
	pWinHttpReceiveResponse                = winhttp.NewProc("WinHttpReceiveResponse")
	pWinHttpQueryHeaders                   = winhttp.NewProc("WinHttpQueryHeaders")
	pWinHttpQueryDataAvailable             = winhttp.NewProc("WinHttpQueryDataAvailable")
	pWinHttpReadData                       = winhttp.NewProc("WinHttpReadData")
	pWinHttpCloseHandle                    = winhttp.NewProc("WinHttpCloseHandle")
	pWinHttpSetTimeouts                    = winhttp.NewProc("WinHttpSetTimeouts")
	pWinHttpGetIEProxyConfigForCurrentUser = winhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
)

const (
	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_VISIBLE          = 0x10000000
	CW_USEDEFAULT       = ^uintptr(0x7fffffff)
	SW_MAXIMIZE         = 3

	WM_CREATE        = 0x0001
	WM_DESTROY       = 0x0002
	WM_SIZE          = 0x0005
	WM_PAINT         = 0x000F
	WM_CLOSE         = 0x0010
	WM_ERASEBKGND    = 0x0014
	WM_SETCURSOR     = 0x0020
	WM_GETMINMAXINFO = 0x0024
	WM_KEYDOWN       = 0x0100
	WM_CHAR          = 0x0102
	WM_TIMER         = 0x0113
	WM_MOUSEMOVE     = 0x0200
	WM_LBUTTONDOWN   = 0x0201
	WM_LBUTTONUP     = 0x0202
	WM_MOUSEWHEEL    = 0x020A
	WM_MOUSELEAVE    = 0x02A3
	WM_DPICHANGED    = 0x02E0
	WM_APP           = 0x8000
	WM_SCAN_DONE     = WM_APP + 1
	WM_DIAG_DONE     = WM_APP + 2
	WM_UPDATE_DONE   = WM_APP + 3

	VK_ESCAPE = 0x1B
	TME_LEAVE = 0x00000002
	IDC_ARROW = 32512
	IDC_HAND  = 32649

	DT_LEFT         = 0x00000000
	DT_CENTER       = 0x00000001
	DT_RIGHT        = 0x00000002
	DT_VCENTER      = 0x00000004
	DT_WORDBREAK    = 0x00000010
	DT_SINGLELINE   = 0x00000020
	DT_END_ELLIPSIS = 0x00008000
	DT_NOPREFIX     = 0x00000800

	TRANSPARENT   = 1
	DC_BRUSH      = 18
	DC_PEN        = 19
	NULL_PEN      = 8
	SRCCOPY       = 0x00CC0020
	SW_SHOWNORMAL = 1

	SWP_NOZORDER   = 0x0004
	SWP_NOACTIVATE = 0x0010

	MOVEFILE_REPLACE_EXISTING = 0x00000001
	MOVEFILE_WRITE_THROUGH    = 0x00000008

	WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY = 4
	WINHTTP_FLAG_SECURE                 = 0x00800000
	WINHTTP_QUERY_STATUS_CODE           = 19
	WINHTTP_QUERY_FLAG_NUMBER           = 0x20000000
	WINHTTP_ADDREQ_FLAG_ADD             = 0x20000000
	WINHTTP_ADDREQ_FLAG_REPLACE         = 0x80000000
)

func utf16Ptr(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func loword(v uintptr) int32    { return int32(uint16(v & 0xffff)) }
func hiword(v uintptr) int32    { return int32(uint16((v >> 16) & 0xffff)) }
func rgb(r, g, b byte) uint32   { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }
func contains(r RECT, x, y int32) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

func replaceFile(tmp, target string) error {
	r, _, e := pMoveFileExW.Call(
		uintptr(unsafe.Pointer(utf16Ptr(tmp))),
		uintptr(unsafe.Pointer(utf16Ptr(target))),
		MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH,
	)
	if r == 0 {
		return e
	}
	return nil
}

// ----------------------------- Application model -----------------------------

type Token struct {
	Chain             string    `json:"chain"`
	ChainID           string    `json:"chain_id"`
	Score             int       `json:"score"`
	Grade             string    `json:"grade"`
	Name              string    `json:"name"`
	Symbol            string    `json:"symbol"`
	Address           string    `json:"address"`
	Price             float64   `json:"price"`
	Liquidity         float64   `json:"liquidity"`
	Volume24          float64   `json:"volume24"`
	AgeHours          float64   `json:"age_hours"`
	Change24          float64   `json:"change24"`
	Buys24            int       `json:"buys24"`
	Sells24           int       `json:"sells24"`
	BuyTaxPct         float64   `json:"buy_tax_pct"`
	SellTaxPct        float64   `json:"sell_tax_pct"`
	TaxKnown          bool      `json:"tax_known"`
	Security          string    `json:"security"`
	Evidence          []string  `json:"evidence"`
	DEXURL            string    `json:"dex_url"`
	Source            string    `json:"source"`
	PoolAddress       string    `json:"pool_address,omitempty"`
	BlockNumber       uint64    `json:"block_number,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
	MetadataCheckedAt time.Time `json:"metadata_checked_at,omitempty"`
}

type appState struct {
	hwnd              HWND
	mu                sync.Mutex
	bb                backBuffer
	dpi               int32
	uiScale           float64
	scaleIndex        int
	hover             int
	pressed           int
	mouseTracking     bool
	scanning          bool
	scanCancel        context.CancelFunc
	spinner           int
	autoRefresh       bool // V2.4: 5-second free real-time monitor
	lastScan          time.Time
	lastPollTry       time.Time
	lastHeartbeatLog  time.Time
	latestBlock       uint64
	chainSummary      string
	activeChains      int
	results           []Token
	selected          int
	listPage          int // derived page number retained for the page buttons
	listOffset        int // first visible filtered row; mouse wheel moves this continuously
	pageScroll        int32
	detailScroll      int32
	filterMode        int // 0 all, 1 watched, 2 verified, 3 waiting data
	sortMode          int // 0 score, 1 newest, 2 liquidity
	searchText        string
	searchFocused     bool
	candidatePool     int
	lastBatchAnalyzed int
	lastScanDuration  time.Duration
	lastSuccess       time.Time
	page              int // 0 radar, 1 paper trading
	sim               *SimState
	monitor           *MonitorState
	selectedPos       int
	selectedTrade     int
	resetConfirmUntil time.Time
	logs              []string
	status            string
	statusKind        int // 0 neutral, 1 success, 2 warning, 3 error
	modal             int // 0 none, 1 help, 2 diagnostics, 3 expanded token details
	diagText          string
	toast             string
	toastUntil        time.Time
	dirty             bool
	scanSeq           uint64
	activeScanID      uint64
	updateChecking    bool
	updateInfo        updateInfo
	lastUpdateCheck   time.Time
}

var app = &appState{dpi: 96, uiScale: 1.0, selected: -1, selectedPos: -1, selectedTrade: -1, status: "等待多链实时监控", logs: []string{}, dirty: true, autoRefresh: true, sim: NewSimState(), monitor: NewMonitorState()}

const (
	idNone         = 0
	idScan         = 1
	idStop         = 2
	idDemo         = 3
	idExport       = 4
	idOpenDEX      = 5
	idTutorial     = 6
	idDiagnose     = 7
	idScaleDown    = 8
	idScaleUp      = 9
	idAuto         = 10
	idModalClose   = 11
	idModalPrev    = 12
	idModalNext    = 13
	idPageRadar    = 14
	idPageSim      = 15
	idSimBuy       = 16
	idSimSell      = 17
	idSimCloseAll  = 18
	idSimAuto      = 19
	idSimReset     = 20
	idSimExport    = 21
	idFilterAll    = 22
	idFilterWatch  = 23
	idFilterSafe   = 24
	idFilterWait   = 25
	idSortMode     = 26
	idSearchBox    = 27
	idClearSearch  = 28
	idPrevPage     = 29
	idNextPage     = 30
	idSimProfile   = 31
	idUpdate       = 32
	idWatchToggle  = 33
	idMonitorCSV   = 34
	idDetailChart  = 35
	idDetailChain  = 36
	idExpandDetail = 37
	idModalChart   = 38
	idModalChain   = 39
	idRowBase      = 1000
	idPosBase      = 2000
	idTradeBase    = 3000
	idChartBase    = 4000
	idChainBase    = 5000
)

type layout struct {
	client         RECT
	header         RECT
	toolbar        RECT
	statusPill     RECT
	buttons        map[int]RECT
	cards          [4]RECT
	banner         RECT
	table          RECT
	filterBar      RECT
	searchBox      RECT
	pageFooter     RECT
	tableHeader    RECT
	detail         RECT
	logs           RECT
	simPositions   RECT
	simPosHeader   RECT
	simTrades      RECT
	simTradeHeader RECT
	statusbar      RECT
	modal          RECT
}

func s(v float64) int32          { return int32(math.Round(v * float64(app.dpi) / 96.0 * app.uiScale)) }
func rect(x, y, w, h int32) RECT { return RECT{x, y, x + w, y + h} }
func inset(r RECT, n int32) RECT { return RECT{r.Left + n, r.Top + n, r.Right - n, r.Bottom - n} }
func width(r RECT) int32         { return r.Right - r.Left }
func height(r RECT) int32        { return r.Bottom - r.Top }

func pageScrollRange() int32 { return s(360) }

func clampPageScroll() {
	if app.pageScroll < 0 {
		app.pageScroll = 0
	}
	if maxScroll := pageScrollRange(); app.pageScroll > maxScroll {
		app.pageScroll = maxScroll
	}
}

func scrollPage(delta int32) bool {
	before := app.pageScroll
	app.pageScroll += delta
	clampPageScroll()
	return app.pageScroll != before
}

func shiftRectY(r RECT, delta int32) RECT {
	r.Top += delta
	r.Bottom += delta
	return r
}

func buildLayout() layout {
	var cr RECT
	pGetClientRect.Call(uintptr(app.hwnd), uintptr(unsafe.Pointer(&cr)))
	W, H := width(cr), height(cr)
	virtualH := H + pageScrollRange()
	pad := s(16)
	headerH := s(78)
	toolbarH := s(68)
	cardH := s(94)
	bannerH := s(42)
	statusH := s(28)
	gap := s(12)
	l := layout{client: cr, buttons: map[int]RECT{}}
	l.header = rect(0, 0, W, headerH)
	l.statusPill = rect(W-s(210), s(20), s(190), s(38))
	l.buttons[idTutorial] = rect(l.statusPill.Left-s(94), s(20), s(82), s(38))
	l.buttons[idUpdate] = rect(l.buttons[idTutorial].Left-s(94), s(20), s(82), s(38))
	l.buttons[idPageSim] = rect(l.buttons[idUpdate].Left-s(124), s(20), s(112), s(38))
	l.buttons[idPageRadar] = rect(l.buttons[idPageSim].Left-s(108), s(20), s(96), s(38))
	l.toolbar = rect(pad, headerH, W-pad*2, toolbarH)
	bx := l.toolbar.Left + s(14)
	by := l.toolbar.Top + s(14)
	bh := s(40)
	btns := []struct {
		id int
		w  float64
	}{{idScan, 112}, {idStop, 112}, {idDemo, 112}, {idExport, 112}, {idOpenDEX, 112}, {idDiagnose, 112}}
	for _, b := range btns {
		l.buttons[b.id] = rect(bx, by, s(b.w), bh)
		bx += s(b.w + 10)
	}
	autoW := s(190)
	l.buttons[idAuto] = rect(l.toolbar.Right-autoW-s(14), by, autoW, bh)
	l.buttons[idScaleUp] = rect(l.buttons[idAuto].Left-s(52), by, s(42), bh)
	l.buttons[idScaleDown] = rect(l.buttons[idScaleUp].Left-s(96), by, s(42), bh)
	// Simulation-page actions reuse the same toolbar space.
	sx := l.toolbar.Left + s(14)
	for _, b := range []struct {
		id int
		w  float64
	}{{idSimSell, 118}, {idSimCloseAll, 118}, {idSimExport, 118}, {idSimReset, 118}} {
		l.buttons[b.id] = rect(sx, by, s(b.w), bh)
		sx += s(b.w + 10)
	}
	l.buttons[idSimAuto] = rect(l.toolbar.Right-s(220), by, s(206), bh)
	l.buttons[idSimProfile] = rect(l.buttons[idSimAuto].Left-s(154), by, s(144), bh)
	// Manual paper buy is available on the radar page.
	l.buttons[idSimBuy] = rect(l.buttons[idDiagnose].Right+s(10), by, s(118), bh)
	// scale indicator occupies gap between A-/A+
	cardsTop := l.toolbar.Bottom + gap
	totalW := W - pad*2
	cardGap := s(10)
	cardW := (totalW - cardGap*3) / 4
	for i := 0; i < 4; i++ {
		l.cards[i] = rect(pad+int32(i)*(cardW+cardGap), cardsTop, cardW, cardH)
	}
	l.banner = rect(pad, cardsTop+cardH+gap, totalW, bannerH)
	contentTop := l.banner.Bottom + gap
	contentBottom := virtualH - statusH - gap
	rightW := s(390)
	if W < s(1250) {
		rightW = s(330)
	}
	rightH := contentBottom - contentTop
	logH := s(190)
	if rightH > s(720) {
		logH = s(215)
	}
	if logH > rightH-s(330) {
		logH = rightH - s(330)
	}
	if logH < s(150) {
		logH = s(150)
	}
	detailH := rightH - gap - logH
	l.detail = rect(W-pad-rightW, contentTop, rightW, detailH)
	l.logs = rect(l.detail.Left, l.detail.Bottom+gap, rightW, logH)
	l.buttons[idExpandDetail] = rect(l.detail.Right-s(92), l.detail.Top+s(8), s(76), s(34))
	l.buttons[idWatchToggle] = rect(l.buttons[idExpandDetail].Left-s(102), l.detail.Top+s(8), s(94), s(34))
	linkGap := s(8)
	linkW := (rightW - s(36) - linkGap) / 2
	l.buttons[idDetailChart] = rect(l.detail.Left+s(18), l.detail.Top+s(96), linkW, s(34))
	l.buttons[idDetailChain] = rect(l.buttons[idDetailChart].Right+linkGap, l.detail.Top+s(96), linkW, s(34))
	l.buttons[idMonitorCSV] = rect(l.logs.Right-s(126), l.logs.Top+s(8), s(110), s(34))
	l.table = rect(pad, contentTop, l.detail.Left-pad-gap, contentBottom-contentTop)
	compactFilters := width(l.table) < s(920)
	filterBarH := s(42)
	if compactFilters {
		filterBarH = s(80)
	}
	l.filterBar = rect(l.table.Left+s(12), l.table.Top+s(48), width(l.table)-s(24), filterBarH)
	filterX := l.filterBar.Left
	filterSpecs := []struct {
		id int
		w  float64
	}{{idFilterAll, 60}, {idFilterWatch, 72}, {idFilterSafe, 72}, {idFilterWait, 72}, {idSortMode, 98}}
	gapW := s(7)
	desired := make([]int32, len(filterSpecs))
	var desiredTotal int32
	for i, b := range filterSpecs {
		desired[i] = s(b.w)
		desiredTotal += desired[i]
	}
	rowAvail := width(l.filterBar) - gapW*int32(len(filterSpecs)-1)
	shrink := 1.0
	if desiredTotal > rowAvail && rowAvail > 0 {
		shrink = float64(rowAvail) / float64(desiredTotal)
	}
	for i, b := range filterSpecs {
		bw := int32(math.Floor(float64(desired[i]) * shrink))
		if bw < s(48) {
			bw = s(48)
		}
		l.buttons[b.id] = rect(filterX, l.filterBar.Top+s(2), bw, s(36))
		filterX += bw
		if i+1 < len(filterSpecs) {
			filterX += gapW
		}
	}
	searchW := s(250)
	searchTop := l.filterBar.Top + s(2)
	if compactFilters {
		searchTop = l.filterBar.Top + s(42)
		searchW = width(l.filterBar)
	} else {
		available := l.filterBar.Right - filterX - s(8)
		if searchW > available {
			searchW = available
		}
		if searchW < s(150) {
			searchW = s(150)
		}
	}
	l.searchBox = rect(l.filterBar.Right-searchW, searchTop, searchW, s(36))
	l.buttons[idSearchBox] = l.searchBox
	l.buttons[idClearSearch] = rect(l.searchBox.Right-s(34), l.searchBox.Top+s(1), s(32), s(34))
	l.tableHeader = rect(l.table.Left+s(12), l.filterBar.Bottom+s(4), width(l.table)-s(24), s(38))
	l.pageFooter = rect(l.table.Left+s(12), l.table.Bottom-s(42), width(l.table)-s(24), s(34))
	l.buttons[idPrevPage] = rect(l.pageFooter.Right-s(190), l.pageFooter.Top, s(84), s(32))
	l.buttons[idNextPage] = rect(l.pageFooter.Right-s(92), l.pageFooter.Top, s(84), s(32))
	posH := (height(l.table) - gap) / 2
	l.simPositions = rect(l.table.Left, l.table.Top, width(l.table), posH)
	l.simPosHeader = rect(l.simPositions.Left+s(12), l.simPositions.Top+s(50), width(l.simPositions)-s(24), s(38))
	l.simTrades = rect(l.table.Left, l.simPositions.Bottom+gap, width(l.table), height(l.table)-posH-gap)
	l.simTradeHeader = rect(l.simTrades.Left+s(12), l.simTrades.Top+s(50), width(l.simTrades)-s(24), s(38))
	clampPageScroll()
	pageDelta := -app.pageScroll
	l.header = shiftRectY(l.header, pageDelta)
	l.toolbar = shiftRectY(l.toolbar, pageDelta)
	l.statusPill = shiftRectY(l.statusPill, pageDelta)
	for i := range l.cards {
		l.cards[i] = shiftRectY(l.cards[i], pageDelta)
	}
	l.banner = shiftRectY(l.banner, pageDelta)
	l.table = shiftRectY(l.table, pageDelta)
	l.filterBar = shiftRectY(l.filterBar, pageDelta)
	l.searchBox = shiftRectY(l.searchBox, pageDelta)
	l.pageFooter = shiftRectY(l.pageFooter, pageDelta)
	l.tableHeader = shiftRectY(l.tableHeader, pageDelta)
	l.detail = shiftRectY(l.detail, pageDelta)
	l.logs = shiftRectY(l.logs, pageDelta)
	l.simPositions = shiftRectY(l.simPositions, pageDelta)
	l.simPosHeader = shiftRectY(l.simPosHeader, pageDelta)
	l.simTrades = shiftRectY(l.simTrades, pageDelta)
	l.simTradeHeader = shiftRectY(l.simTradeHeader, pageDelta)
	for id, r := range l.buttons {
		l.buttons[id] = shiftRectY(r, pageDelta)
	}
	l.statusbar = rect(0, H-statusH, W, statusH)
	mw := min32(s(760), W-s(80))
	mh := min32(s(560), H-s(80))
	if app.modal == 3 {
		mw = min32(s(1050), W-s(80))
		mh = min32(s(720), H-s(80))
	}
	l.modal = rect((W-mw)/2, (H-mh)/2, mw, mh)
	l.buttons[idModalClose] = rect(l.modal.Right-s(52), l.modal.Top+s(18), s(34), s(34))
	l.buttons[idModalNext] = rect(l.modal.Right-s(154), l.modal.Bottom-s(64), s(130), s(42))
	l.buttons[idModalChart] = rect(l.modal.Right-s(330), l.modal.Top+s(20), s(120), s(38))
	l.buttons[idModalChain] = rect(l.modal.Right-s(200), l.modal.Top+s(20), s(130), s(38))
	return l
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func radarPageSize(l layout) int {
	rowH := radarRowHeight()
	available := l.pageFooter.Top - (l.tableHeader.Bottom + s(4)) - s(4)
	n := int(available / rowH)
	if n < 1 {
		n = 1
	}
	return n
}

func radarRowHeight() int32 { return s(68) }

func filteredResultIndices() []int {
	query := strings.ToLower(strings.TrimSpace(app.searchText))
	idxs := make([]int, 0, len(app.results))
	for i, t := range app.results {
		match := true
		switch app.filterMode {
		case 1:
			match = app.monitor != nil && app.monitor.IsWatching(t)
		case 2:
			match = t.Security == "已验证"
		case 3:
			match = t.Grade == "等待市场数据" || t.Grade == "等待分析" || t.Price <= 0
		}
		if match && query != "" {
			hay := strings.ToLower(strings.Join([]string{t.Chain, t.Symbol, t.Name, t.Address, t.Source, t.Security, t.Grade}, " "))
			match = strings.Contains(hay, query)
		}
		if match {
			idxs = append(idxs, i)
		}
	}
	sort.SliceStable(idxs, func(i, j int) bool {
		a, b := app.results[idxs[i]], app.results[idxs[j]]
		switch app.sortMode {
		case 1:
			if a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.Score > b.Score
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		case 2:
			if a.Liquidity == b.Liquidity {
				return a.Score > b.Score
			}
			return a.Liquidity > b.Liquidity
		default:
			if a.Score == b.Score {
				return a.UpdatedAt.After(b.UpdatedAt)
			}
			return a.Score > b.Score
		}
	})
	return idxs
}

func clampListPage(l layout) {
	idxs := filteredResultIndices()
	pageSize := radarPageSize(l)
	maxOffset := len(idxs) - pageSize
	if maxOffset < 0 {
		maxOffset = 0
	}
	if app.listOffset < 0 {
		app.listOffset = 0
	}
	if app.listOffset > maxOffset {
		app.listOffset = maxOffset
	}
	app.listPage = app.listOffset / pageSize
}

func visibleResultIndices(l layout) ([]int, int, int) {
	clampListPage(l)
	idxs := filteredResultIndices()
	pageSize := radarPageSize(l)
	pages := 1
	if len(idxs) > 0 {
		pages = (len(idxs) + pageSize - 1) / pageSize
	}
	start := app.listOffset
	if start > len(idxs) {
		start = len(idxs)
	}
	end := start + pageSize
	if end > len(idxs) {
		end = len(idxs)
	}
	return idxs[start:end], len(idxs), pages
}

func resetListPosition() {
	app.listOffset = 0
	app.listPage = 0
}

func scrollListRows(l layout, rows int) bool {
	before := app.listOffset
	app.listOffset += rows
	clampListPage(l)
	return app.listOffset != before
}

func selectFirstVisible(l layout) {
	visible, _, _ := visibleResultIndices(l)
	if len(visible) == 0 {
		app.selected = -1
		return
	}
	for _, idx := range visible {
		if idx == app.selected {
			return
		}
	}
	app.selected = visible[0]
}

func sortModeText() string {
	switch app.sortMode {
	case 1:
		return "最新优先"
	case 2:
		return "流动性优先"
	default:
		return "评分优先"
	}
}

// ----------------------------- GDI rendering -----------------------------

type fonts struct{ title, subtitle, button, body, small, number, section, table HFONT }

var fnts fonts
var fontKey string

func deleteFonts() {
	for _, f := range []HFONT{fnts.title, fnts.subtitle, fnts.button, fnts.body, fnts.small, fnts.number, fnts.section, fnts.table} {
		if f != 0 {
			pDeleteObject.Call(uintptr(f))
		}
	}
	fnts = fonts{}
}

func makeFont(px int32, weight int32) HFONT {
	name := utf16Ptr("Microsoft YaHei UI")
	h, _, _ := pCreateFontW.Call(uintptr(-px), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(name)))
	return HFONT(h)
}

func ensureFonts() {
	key := fmt.Sprintf("%d-%.2f", app.dpi, app.uiScale)
	if key == fontKey {
		return
	}
	deleteFonts()
	fontKey = key
	fnts.title = makeFont(s(30), 700)
	fnts.subtitle = makeFont(s(14), 400)
	fnts.button = makeFont(s(16), 600)
	fnts.body = makeFont(s(15), 400)
	fnts.small = makeFont(s(13), 400)
	fnts.number = makeFont(s(30), 700)
	fnts.section = makeFont(s(19), 700)
	fnts.table = makeFont(s(14), 500)
}

func ensureBackBuffer(hdc HDC, w, h int32) {
	if app.bb.dc != 0 && app.bb.w == w && app.bb.h == h {
		return
	}
	if app.bb.dc != 0 {
		if app.bb.old != 0 {
			pSelectObject.Call(uintptr(app.bb.dc), uintptr(app.bb.old))
		}
		if app.bb.bmp != 0 {
			pDeleteObject.Call(uintptr(app.bb.bmp))
		}
		pDeleteDC.Call(uintptr(app.bb.dc))
		app.bb = backBuffer{}
	}
	dc, _, _ := pCreateCompatibleDC.Call(uintptr(hdc))
	bmp, _, _ := pCreateCompatibleBitmap.Call(uintptr(hdc), uintptr(w), uintptr(h))
	old, _, _ := pSelectObject.Call(dc, bmp)
	app.bb = backBuffer{dc: HDC(dc), bmp: HBITMAP(bmp), old: HGDIOBJ(old), w: w, h: h}
}

func dcBrush(dc HDC, c uint32) {
	obj, _, _ := pGetStockObject.Call(DC_BRUSH)
	pSelectObject.Call(uintptr(dc), obj)
	pSetDCBrushColor.Call(uintptr(dc), uintptr(c))
}
func dcPen(dc HDC, c uint32) {
	obj, _, _ := pGetStockObject.Call(DC_PEN)
	pSelectObject.Call(uintptr(dc), obj)
	pSetDCPenColor.Call(uintptr(dc), uintptr(c))
}
func fillRect(dc HDC, r RECT, c uint32) {
	dcBrush(dc, c)
	dcPen(dc, c)
	pRectangle.Call(uintptr(dc), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right+1), uintptr(r.Bottom+1))
}
func roundRect(dc HDC, r RECT, fill, border uint32, radius int32) {
	dcBrush(dc, fill)
	dcPen(dc, border)
	pRoundRect.Call(uintptr(dc), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom), uintptr(radius), uintptr(radius))
}
func ellipse(dc HDC, r RECT, fill, border uint32) {
	dcBrush(dc, fill)
	dcPen(dc, border)
	pEllipse.Call(uintptr(dc), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
}
func line(dc HDC, x1, y1, x2, y2 int32, c uint32) {
	dcPen(dc, c)
	pMoveToEx.Call(uintptr(dc), uintptr(x1), uintptr(y1), 0)
	pLineTo.Call(uintptr(dc), uintptr(x2), uintptr(y2))
}
func withClip(dc HDC, r RECT, draw func()) {
	if width(r) <= 0 || height(r) <= 0 {
		return
	}
	saved, _, _ := pSaveDC.Call(uintptr(dc))
	if saved == 0 {
		draw()
		return
	}
	pIntersectClipRect.Call(uintptr(dc), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
	draw()
	pRestoreDC.Call(uintptr(dc), saved)
}
func text(dc HDC, txt string, r RECT, font HFONT, c uint32, flags uint32) {
	if txt == "" {
		return
	}
	pSelectObject.Call(uintptr(dc), uintptr(font))
	pSetBkMode.Call(uintptr(dc), TRANSPARENT)
	pSetTextColor.Call(uintptr(dc), uintptr(c))
	u, _ := syscall.UTF16FromString(txt)
	if len(u) > 0 {
		pDrawTextW.Call(uintptr(dc), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), uintptr(flags|DT_NOPREFIX))
	}
}

var col = struct {
	bg, panel, panel2, panel3, border, border2, text, muted, dim, cyan, cyan2, green, greenBg, red, redBg, yellow, yellowBg, blue uint32
}{
	rgb(6, 14, 28), rgb(13, 27, 48), rgb(17, 34, 59), rgb(21, 42, 71), rgb(39, 65, 98), rgb(54, 82, 119), rgb(238, 245, 255), rgb(158, 180, 211), rgb(100, 126, 163), rgb(48, 216, 204), rgb(35, 176, 167), rgb(72, 224, 175), rgb(11, 64, 54), rgb(255, 100, 126), rgb(70, 26, 42), rgb(255, 203, 72), rgb(66, 54, 22), rgb(86, 153, 255),
}

func drawButton(dc HDC, id int, r RECT, label string, enabled bool, accent bool) {
	hovered := app.hover == id
	pressed := app.pressed == id
	bg := col.panel2
	border := col.border
	tc := col.text
	if accent {
		bg = col.cyan
		border = col.cyan
		tc = rgb(3, 35, 38)
	}
	if hovered && enabled {
		if accent {
			bg = rgb(68, 232, 220)
		} else {
			bg = col.panel3
			border = col.border2
		}
	}
	if pressed && enabled {
		if accent {
			bg = col.cyan2
		} else {
			bg = rgb(11, 24, 44)
		}
	}
	if !enabled {
		tc = col.dim
		bg = rgb(12, 24, 42)
		border = rgb(27, 46, 70)
	}
	roundRect(dc, r, bg, border, s(8))
	text(dc, label, r, fnts.button, tc, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
}

func drawUI(dc HDC, l layout) {
	if app.page == 1 {
		drawSimUI(dc, l)
		return
	}
	drawRadarUI(dc, l)
}

func drawCommonHeader(dc HDC, l layout, subtitle string) {
	ensureFonts()
	fillRect(dc, l.client, col.bg)
	logo := rect(s(18), l.header.Top+s(18), s(44), s(44))
	ellipse(dc, logo, col.cyan, col.cyan)
	text(dc, "B", logo, fnts.button, rgb(3, 35, 38), DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	text(dc, "Multi-Chain Token Radar", rect(s(76), l.header.Top+s(10), s(500), s(42)), fnts.title, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	text(dc, subtitle, rect(s(77), l.header.Top+s(48), s(560), s(22)), fnts.subtitle, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	drawButton(dc, idPageRadar, l.buttons[idPageRadar], "代币雷达", true, app.page == 0)
	drawButton(dc, idPageSim, l.buttons[idPageSim], "模拟盘", true, app.page == 1)
	statusFill := col.panel2
	dot := col.yellow
	if app.statusKind == 1 {
		dot = col.green
	}
	if app.statusKind == 3 {
		dot = col.red
	}
	roundRect(dc, l.statusPill, statusFill, col.border, s(10))
	ellipse(dc, rect(l.statusPill.Left+s(14), l.statusPill.Top+s(14), s(10), s(10)), dot, dot)
	text(dc, app.status, rect(l.statusPill.Left+s(32), l.statusPill.Top, l.statusPill.Right-l.statusPill.Left-s(38), height(l.statusPill)), fnts.small, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	drawButton(dc, idTutorial, l.buttons[idTutorial], "说明", true, false)
	drawButton(dc, idUpdate, l.buttons[idUpdate], updateButtonText(), !app.updateChecking, app.updateInfo.Available)
}

func drawSimUI(dc HDC, l layout) {
	drawCommonHeader(dc, l, "V2.11 探索样本与拒绝统计 · 自动策略验证 · 不连接钱包")
	if app.sim == nil {
		app.sim = NewSimState()
	}
	m := app.sim.Metrics(tokensToQuotes(app.results, time.Now()))
	v := app.sim.Validation()
	exploreOpen, exploreClosed, explorePnL := app.sim.ExplorationSummary()
	shadowOpen, shadowClosed, shadowWins, shadowAvg := app.sim.ShadowSummary()

	roundRect(dc, l.toolbar, col.panel, col.border, s(9))
	drawButton(dc, idSimSell, l.buttons[idSimSell], "卖出选中", selectedPositionValid(), false)
	drawButton(dc, idSimCloseAll, l.buttons[idSimCloseAll], "全部平仓", len(app.sim.Positions) > 0, false)
	drawButton(dc, idSimExport, l.buttons[idSimExport], "导出记录", len(app.sim.Trades) > 0, false)
	drawButton(dc, idSimReset, l.buttons[idSimReset], "重置模拟盘", true, false)
	drawButton(dc, idSimProfile, l.buttons[idSimProfile], "策略档位："+app.sim.ProfileName(), true, app.sim.AutoProfile == AutoProfileExplore)
	ar := l.buttons[idSimAuto]
	roundRect(dc, ar, col.panel2, col.border, s(8))
	sw := rect(ar.Left+s(12), ar.Top+s(10), s(40), s(20))
	swbg := rgb(75, 94, 122)
	if app.sim.AutoEnabled {
		swbg = col.cyan2
	}
	roundRect(dc, sw, swbg, swbg, s(12))
	knobX := sw.Left + s(3)
	if app.sim.AutoEnabled {
		knobX = sw.Right - s(17)
	}
	ellipse(dc, rect(knobX, sw.Top+s(3), s(14), s(14)), col.text, col.text)
	text(dc, "自动模拟策略", rect(sw.Right+s(10), ar.Top, ar.Right-sw.Right-s(18), height(ar)), fnts.small, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)

	vals := []string{fmt.Sprintf("%.2f", m.Equity), fmt.Sprintf("%.2f", m.Cash), fmt.Sprintf("%+.2f", m.NetPnL), fmt.Sprintf("%d / %d", exploreClosed, shadowClosed)}
	labels := []string{"模拟总资产 USDC", "可用余额", "累计净盈亏", "探索 / 影子闭合样本"}
	subs := []string{fmt.Sprintf("持仓市值 %.2f", m.PositionValue), fmt.Sprintf("当前持仓 %d / %d", len(app.sim.Positions), app.sim.Config.MaxPositions), fmt.Sprintf("最大回撤 %.1f%%", m.MaxDrawdown), fmt.Sprintf("探索进行 %d，净 %+.2f · 影子进行 %d，胜 %d，均值 %+.1f%%", exploreOpen, explorePnL, shadowOpen, shadowWins, shadowAvg)}
	accents := []uint32{col.blue, col.cyan, col.green, col.yellow}
	for i, r := range l.cards {
		roundRect(dc, r, col.panel, col.border, s(9))
		fillRect(dc, rect(r.Left, r.Top, s(4), height(r)), accents[i])
		text(dc, labels[i], rect(r.Left+s(18), r.Top+s(10), width(r)-s(28), s(22)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
		tc := col.text
		if i == 2 && m.NetPnL < 0 {
			tc = col.red
		} else if i == 2 && m.NetPnL > 0 {
			tc = col.green
		}
		text(dc, vals[i], rect(r.Left+s(18), r.Top+s(30), width(r)-s(28), s(38)), fnts.number, tc, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		text(dc, subs[i], rect(r.Left+s(18), r.Bottom-s(27), width(r)-s(28), s(20)), fnts.small, col.dim, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	}
	bf, bb := col.yellowBg, col.yellow
	banner := fmt.Sprintf("自动模拟已关闭 · %s · 样本 %d/%d；真钱交易保持锁定。", v.Status, v.ClosedPositions, v.RequiredPositions)
	if app.sim.AutoEnabled {
		bf, bb = col.panel2, col.cyan
		banner = fmt.Sprintf("自动模拟运行中（%s档）· %s %d/%d · PF %s · 真钱交易锁定。", app.sim.ProfileName(), v.Status, v.ClosedPositions, v.RequiredPositions, v.ProfitFactorText())
	}
	if v.Passed {
		bf, bb = col.greenBg, col.green
		banner = fmt.Sprintf("纸面验证通过：%d 笔完整自动交易 · 净收益 %+.2f · PF %s；仍需继续观察。", v.ClosedPositions, v.NetPnL, v.ProfitFactorText())
	}
	roundRect(dc, l.banner, bf, bb, s(7))
	text(dc, banner, inset(l.banner, s(14)), fnts.body, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)

	drawSimPositions(dc, l)
	drawSimTrades(dc, l)
	roundRect(dc, l.detail, col.panel, col.border, s(9))
	drawSimDetail(dc, l.detail, m)
	roundRect(dc, l.logs, col.panel, col.border, s(9))
	drawLogs(dc, l.logs)
	fillRect(dc, l.statusbar, rgb(5, 12, 23))
	line(dc, 0, l.statusbar.Top, l.statusbar.Right, l.statusbar.Top, col.border)
	ellipse(dc, rect(s(8), l.statusbar.Top+s(9), s(8), s(8)), col.green, col.green)
	text(dc, fmt.Sprintf("模拟盘：不连接钱包、不发送交易 · 最近价格 %s", lastUpdateText()), rect(s(24), l.statusbar.Top, s(920), height(l.statusbar)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	text(dc, fmt.Sprintf("自动策略：%s（%s档） · 实时监控：%s", onOff(app.sim.AutoEnabled), app.sim.ProfileName(), onOff(app.autoRefresh)), rect(l.statusbar.Right-s(430), l.statusbar.Top, s(415), height(l.statusbar)), fnts.small, col.muted, DT_RIGHT|DT_VCENTER|DT_SINGLELINE)
	drawPageScrollbar(dc, l)
	if app.toast != "" && time.Now().Before(app.toastUntil) {
		tr := rect(l.client.Right-s(380), l.client.Bottom-s(88), s(350), s(48))
		roundRect(dc, tr, col.panel3, col.cyan, s(8))
		text(dc, app.toast, inset(tr, s(10)), fnts.small, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	}
	if app.modal != 0 {
		drawModal(dc, l)
	}
}

func drawSimPositions(dc HDC, l layout) {
	r := l.simPositions
	roundRect(dc, r, col.panel, col.border, s(9))
	text(dc, "当前模拟持仓", rect(r.Left+s(16), r.Top+s(10), s(260), s(30)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	text(dc, fmt.Sprintf("%d 个持仓", len(app.sim.Positions)), rect(r.Right-s(220), r.Top+s(12), s(200), s(28)), fnts.small, col.muted, DT_RIGHT|DT_VCENTER|DT_SINGLELINE)
	fillRect(dc, l.simPosHeader, col.panel3)
	headers := []string{"代币", "成本", "现价", "净收益率", "持仓时间"}
	fr := []float64{0.28, 0.18, 0.18, 0.18, 0.18}
	x := l.simPosHeader.Left
	for i, h := range headers {
		cw := int32(float64(width(l.simPosHeader)) * fr[i])
		text(dc, h, rect(x, l.simPosHeader.Top, cw, height(l.simPosHeader)), fnts.table, col.muted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		x += cw
	}
	if len(app.sim.Positions) == 0 {
		text(dc, "暂无持仓。回到“代币雷达”选择候选并点击“模拟买入”。", rect(r.Left+s(20), l.simPosHeader.Bottom+s(15), width(r)-s(40), s(52)), fnts.body, col.muted, DT_CENTER|DT_WORDBREAK)
		return
	}
	quotes := tokensToQuotes(app.results, time.Now())
	qm := quoteMap(quotes)
	y := l.simPosHeader.Bottom + s(4)
	rowH := s(44)
	maxRows := int((r.Bottom - y - s(8)) / rowH)
	if maxRows > len(app.sim.Positions) {
		maxRows = len(app.sim.Positions)
	}
	for i := 0; i < maxRows; i++ {
		p := app.sim.Positions[i]
		rr := rect(l.simPosHeader.Left, y, width(l.simPosHeader), rowH-s(2))
		if i == app.selectedPos {
			fillRect(dc, rr, rgb(19, 50, 77))
		}
		q := qm[simKey(p.Chain, p.Address)]
		priceV, liq := p.CurrentPrice, p.CurrentLiquidity
		if q.Price > 0 {
			priceV, liq = q.Price, q.Liquidity
		}
		net, _ := app.sim.liquidationValue(p, priceV, liq)
		ret := 0.0
		if p.RemainingCost > 0 {
			ret = (net - p.RemainingCost) / p.RemainingCost * 100
		}
		vals := []string{"[" + chainLabel(p.Chain) + "] " + p.Symbol, fmt.Sprintf("%.2f", p.RemainingCost), price(priceV), fmt.Sprintf("%+.1f%%", ret), durationText(time.Since(p.OpenedAt))}
		x = rr.Left
		for j, v := range vals {
			cw := int32(float64(width(rr)) * fr[j])
			tc := col.text
			if j == 3 && ret >= 0 {
				tc = col.green
			} else if j == 3 {
				tc = col.red
			}
			text(dc, v, rect(x+s(5), rr.Top, cw-s(10), height(rr)), fnts.table, tc, DT_CENTER|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
			x += cw
		}
		line(dc, rr.Left, rr.Bottom, rr.Right, rr.Bottom, rgb(25, 47, 74))
		y += rowH
	}
}

func drawSimTrades(dc HDC, l layout) {
	r := l.simTrades
	roundRect(dc, r, col.panel, col.border, s(9))
	text(dc, "模拟交易记录", rect(r.Left+s(16), r.Top+s(10), s(260), s(30)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	text(dc, fmt.Sprintf("最近 %d 条", len(app.sim.Trades)), rect(r.Right-s(220), r.Top+s(12), s(200), s(28)), fnts.small, col.muted, DT_RIGHT|DT_VCENTER|DT_SINGLELINE)
	fillRect(dc, l.simTradeHeader, col.panel3)
	headers := []string{"时间", "代币", "退出原因", "净盈亏", "收益率"}
	fr := []float64{0.18, 0.16, 0.36, 0.16, 0.14}
	x := l.simTradeHeader.Left
	for i, h := range headers {
		cw := int32(float64(width(l.simTradeHeader)) * fr[i])
		text(dc, h, rect(x, l.simTradeHeader.Top, cw, height(l.simTradeHeader)), fnts.table, col.muted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		x += cw
	}
	if len(app.sim.Trades) == 0 {
		return
	}
	y := l.simTradeHeader.Bottom + s(4)
	rowH := s(42)
	maxRows := int((r.Bottom - y - s(8)) / rowH)
	if maxRows > len(app.sim.Trades) {
		maxRows = len(app.sim.Trades)
	}
	for row := 0; row < maxRows; row++ {
		idx := len(app.sim.Trades) - 1 - row
		t := app.sim.Trades[idx]
		rr := rect(l.simTradeHeader.Left, y, width(l.simTradeHeader), rowH-s(2))
		if idx == app.selectedTrade {
			fillRect(dc, rr, rgb(19, 50, 77))
		}
		vals := []string{t.ClosedAt.Format("01-02 15:04"), "[" + chainLabel(t.Chain) + "] " + t.Symbol, t.Reason, fmt.Sprintf("%+.2f", t.PnL), fmt.Sprintf("%+.1f%%", t.PnLPct)}
		x = rr.Left
		for j, v := range vals {
			cw := int32(float64(width(rr)) * fr[j])
			tc := col.text
			if j >= 3 && t.PnL >= 0 {
				tc = col.green
			} else if j >= 3 {
				tc = col.red
			}
			text(dc, v, rect(x+s(5), rr.Top, cw-s(10), height(rr)), fnts.table, tc, DT_CENTER|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
			x += cw
		}
		line(dc, rr.Left, rr.Bottom, rr.Right, rr.Bottom, rgb(25, 47, 74))
		y += rowH
	}
}

func drawSimDetail(dc HDC, r RECT, m SimMetrics) {
	text(dc, "策略验证与风控", rect(r.Left+s(16), r.Top+s(10), width(r)-s(32), s(30)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	line(dc, r.Left+s(14), r.Top+s(48), r.Right-s(14), r.Top+s(48), col.border)
	body := rect(r.Left+s(16), r.Top+s(58), width(r)-s(32), height(r)-s(70))
	y := body.Top
	rules := app.sim.rules()
	v := app.sim.Validation()
	diag := app.sim.LastEntryStats
	exploreOpen, exploreClosed, explorePnL := app.sim.ExplorationSummary()
	shadowOpen, shadowClosed, shadowWins, shadowAvg := app.sim.ShadowSummary()
	positionCap := app.sim.Config.PositionSize
	if app.sim.AutoProfile == AutoProfileExplore && positionCap > 1 {
		positionCap = 1
	}
	securityLine := "• 需要安全已验证，税费必须已确认"
	if !rules.RequireVerified {
		securityLine = "• 测试档允许安全未验证/税费未知，但严重风险仍硬性禁止"
	}
	if app.sim.AutoProfile == AutoProfileExplore {
		securityLine = "• 探索档只用 ≤1 USDC 纸面仓位；严重风险、零分和已知高税费仍硬性禁止"
	}
	patternLine := "• 需要回调后重新走强，避免贴近短线高点"
	if !rules.RequirePullback {
		patternLine = "• 测试档只要求基础上涨趋势，目的是尽快验证模拟系统"
	}
	if app.sim.AutoProfile == AutoProfileExplore {
		patternLine = "• 探索档观察 30 秒；只要基础价格不走弱即可记录小额纸面样本"
	}
	flowLine := fmt.Sprintf("• 已知买卖税≤%.0f%%，买卖笔数比≥%.2f", rules.MaxTaxPct, rules.MinBuySellRatio)
	if rules.MinBuySellRatio == 0 {
		flowLine = fmt.Sprintf("• 已知买卖税≤%.0f%%；不以买卖笔数为硬门槛", rules.MaxTaxPct)
	}
	lines := []string{fmt.Sprintf("正式验证：%s · 严格完整交易 %d/%d", v.Status, v.ClosedPositions, v.RequiredPositions), fmt.Sprintf("严格策略净收益：%+.2f · PF %s · 期望/笔 %+.3f", v.NetPnL, v.ProfitFactorText(), v.Expectancy), fmt.Sprintf("本轮评估：候选 %d · 合格 %d · 探索开仓 %d · 新影子 %d", diag.Evaluated, diag.Eligible, diag.Opened, diag.ShadowStarted), "拦截原因：" + diag.TopReasons(3), fmt.Sprintf("探索样本：进行 %d · 已闭合 %d · 净盈亏 %+.2f（不计正式验证）", exploreOpen, exploreClosed, explorePnL), fmt.Sprintf("影子样本：进行 %d · 已闭合 %d · 胜 %d · 平均变动 %+.1f%%", shadowOpen, shadowClosed, shadowWins, shadowAvg), "", fmt.Sprintf("当前自动策略：%s档 · 动态仓位≤%.2f USDC", app.sim.ProfileName(), positionCap), fmt.Sprintf("• 评分≥%d，流动性≥%s，观察≥%.1f分钟，池龄≤%.0f小时", rules.MinScore, money(rules.MinLiquidity), rules.ObserveMinutes, rules.MaxAgeHours), securityLine, flowLine, patternLine, "• 同链只开一个仓位；流动性不稳或短时暴涨仍拒绝追入", "", "成本已计入 DEX 费、Gas、税费和流动性滑点；探索与影子结果不能当作盈利证明。"}
	for _, ln := range lines {
		h := s(25)
		if ln == "" {
			y += s(10)
			continue
		}
		text(dc, ln, rect(body.Left, y, width(body), h), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		y += h
	}
}

func durationText(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d分", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1f时", d.Hours())
	}
	return fmt.Sprintf("%.1f天", d.Hours()/24)
}

func drawRadarUI(dc HDC, l layout) {
	ensureFonts()
	fillRect(dc, l.client, col.bg)
	// Header
	logo := rect(s(18), l.header.Top+s(18), s(44), s(44))
	ellipse(dc, logo, col.cyan, col.cyan)
	text(dc, "B", logo, fnts.button, rgb(3, 35, 38), DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	text(dc, "Multi-Chain Token Radar", rect(s(76), l.header.Top+s(10), s(500), s(42)), fnts.title, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	text(dc, "Base + BSC + Arbitrum 新池发现 · 免费监控 · 本地模拟交易", rect(s(77), l.header.Top+s(48), s(560), s(22)), fnts.subtitle, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	drawButton(dc, idPageRadar, l.buttons[idPageRadar], "代币雷达", true, app.page == 0)
	drawButton(dc, idPageSim, l.buttons[idPageSim], "模拟盘", true, app.page == 1)
	statusFill := col.panel2
	dot := col.yellow
	if app.statusKind == 1 {
		dot = col.green
	}
	if app.statusKind == 3 {
		dot = col.red
	}
	roundRect(dc, l.statusPill, statusFill, col.border, s(10))
	ellipse(dc, rect(l.statusPill.Left+s(14), l.statusPill.Top+s(14), s(10), s(10)), dot, dot)
	text(dc, app.status, rect(l.statusPill.Left+s(32), l.statusPill.Top, l.statusPill.Right-l.statusPill.Left-s(38), height(l.statusPill)), fnts.small, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	drawButton(dc, idTutorial, l.buttons[idTutorial], "说明", true, false)
	// Toolbar
	roundRect(dc, l.toolbar, col.panel, col.border, s(9))
	drawButton(dc, idScan, l.buttons[idScan], "立即扫描", !app.scanning, true)
	drawButton(dc, idStop, l.buttons[idStop], "停止扫描", app.scanning, false)
	drawButton(dc, idDemo, l.buttons[idDemo], "演示数据", !app.scanning, false)
	drawButton(dc, idExport, l.buttons[idExport], "导出 CSV", len(app.results) > 0, false)
	drawButton(dc, idOpenDEX, l.buttons[idOpenDEX], "选中走势图", app.selected >= 0 && app.selected < len(app.results), false)
	drawButton(dc, idDiagnose, l.buttons[idDiagnose], "连接诊断", !app.scanning, false)
	drawButton(dc, idSimBuy, l.buttons[idSimBuy], "模拟买入", canManualSimBuy(), false)
	drawButton(dc, idScaleDown, l.buttons[idScaleDown], "A−", app.scaleIndex > 0, false)
	drawButton(dc, idScaleUp, l.buttons[idScaleUp], "A+", app.scaleIndex < 3, false)
	indicator := rect(l.buttons[idScaleDown].Right+s(8), l.buttons[idScaleDown].Top, s(62), height(l.buttons[idScaleDown]))
	roundRect(dc, indicator, rgb(10, 23, 42), col.border, s(8))
	text(dc, fmt.Sprintf("%d%%", []int{100, 115, 130, 150}[app.scaleIndex]), indicator, fnts.small, col.muted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	// Auto switch
	ar := l.buttons[idAuto]
	roundRect(dc, ar, col.panel2, col.border, s(8))
	sw := rect(ar.Left+s(12), ar.Top+s(10), s(40), s(20))
	swbg := rgb(75, 94, 122)
	if app.autoRefresh {
		swbg = col.cyan2
	}
	roundRect(dc, sw, swbg, swbg, s(12))
	knobX := sw.Left + s(3)
	if app.autoRefresh {
		knobX = sw.Right - s(17)
	}
	ellipse(dc, rect(knobX, sw.Top+s(3), s(14), s(14)), col.text, col.text)
	text(dc, "5秒实时监控", rect(sw.Right+s(10), ar.Top, ar.Right-sw.Right-s(18), height(ar)), fnts.small, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	// Cards
	vals := []string{strconv.Itoa(len(app.results)), verifiedCount(), watchCount(), strconv.Itoa(app.candidatePool)}
	labels := []string{"当前列表", "安全已验证", "自选关注", "多链候选库"}
	subs := []string{"每条数据标记来源和时间", "第三方结果仍需人工复核", "持续快照与异常告警", "三链最多保留 240 个"}
	accents := []uint32{col.blue, col.cyan, col.green, col.yellow}
	for i, r := range l.cards {
		roundRect(dc, r, col.panel, col.border, s(9))
		fillRect(dc, rect(r.Left, r.Top, s(4), height(r)), accents[i])
		text(dc, labels[i], rect(r.Left+s(18), r.Top+s(10), width(r)-s(28), s(22)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
		text(dc, vals[i], rect(r.Left+s(18), r.Top+s(30), width(r)-s(28), s(38)), fnts.number, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		text(dc, subs[i], rect(r.Left+s(18), r.Bottom-s(27), width(r)-s(28), s(20)), fnts.small, col.dim, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	}
	// Banner
	bf, bb, bd := col.greenBg, col.green, col.green
	next := "—"
	if app.autoRefresh && !app.scanning {
		remain := 5*time.Second - time.Since(app.lastPollTry)
		if remain < 0 {
			remain = 0
		}
		next = fmt.Sprintf("%.0f秒", math.Ceil(remain.Seconds()))
	}
	dur := "—"
	if app.lastScanDuration > 0 {
		dur = fmt.Sprintf("%.1f秒", app.lastScanDuration.Seconds())
	}
	lastOK := "尚未成功"
	if !app.lastSuccess.IsZero() {
		lastOK = app.lastSuccess.Format("15:04:05")
	}
	chainText := app.chainSummary
	if chainText == "" {
		chainText = "等待三链区块状态"
	}
	bannerText := fmt.Sprintf("%s · 上次成功 %s · 本轮分析 %d 个 · 耗时 %s · 下轮 %s", chainText, lastOK, app.lastBatchAnalyzed, dur, next)
	if app.scanning {
		bannerText = fmt.Sprintf("正在并行扫描 Base / BSC / Arbitrum · 已保留 %d 个多链候选", app.candidatePool)
	}
	if app.statusKind == 3 {
		bf = col.redBg
		bb = col.red
		bd = col.red
		bannerText = "所有链或补充接口暂时不可用；程序已保留旧结果，可点击“连接诊断”查看原因。"
	}
	roundRect(dc, l.banner, bf, bb, s(7))
	ellipse(dc, rect(l.banner.Left+s(14), l.banner.Top+s(15), s(10), s(10)), bd, bd)
	text(dc, bannerText, rect(l.banner.Left+s(34), l.banner.Top, l.banner.Right-l.banner.Left-s(44), height(l.banner)), fnts.body, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	// Main panels
	roundRect(dc, l.table, col.panel, col.border, s(9))
	roundRect(dc, l.detail, col.panel, col.border, s(9))
	roundRect(dc, l.logs, col.panel, col.border, s(9))
	text(dc, "候选列表", rect(l.table.Left+s(16), l.table.Top+s(10), s(240), s(30)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	visible, filteredCount, _ := visibleResultIndices(l)
	startNo := 0
	endNo := 0
	if filteredCount > 0 {
		startNo = app.listOffset + 1
		endNo = startNo + len(visible) - 1
	}
	text(dc, fmt.Sprintf("显示 %d–%d / 共 %d 条 · 候选库 %d", startNo, endNo, filteredCount, app.candidatePool), rect(l.table.Right-s(390), l.table.Top+s(12), s(370), s(28)), fnts.small, col.muted, DT_RIGHT|DT_VCENTER|DT_SINGLELINE)
	drawButton(dc, idFilterAll, l.buttons[idFilterAll], "全部", true, app.filterMode == 0)
	drawButton(dc, idFilterWatch, l.buttons[idFilterWatch], "已关注", true, app.filterMode == 1)
	drawButton(dc, idFilterSafe, l.buttons[idFilterSafe], "已验证", true, app.filterMode == 2)
	drawButton(dc, idFilterWait, l.buttons[idFilterWait], "待数据", true, app.filterMode == 3)
	drawButton(dc, idSortMode, l.buttons[idSortMode], sortModeText(), true, false)
	searchFill := col.panel3
	searchBorder := col.border
	if app.searchFocused {
		searchBorder = col.cyan
	}
	roundRect(dc, l.searchBox, searchFill, searchBorder, s(7))
	searchLabel := app.searchText
	searchColor := col.text
	if searchLabel == "" {
		searchLabel = "搜索代币/合约"
		searchColor = col.dim
	}
	text(dc, searchLabel, rect(l.searchBox.Left+s(12), l.searchBox.Top, width(l.searchBox)-s(48), height(l.searchBox)), fnts.small, searchColor, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	if app.searchText != "" {
		text(dc, "×", l.buttons[idClearSearch], fnts.body, col.muted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	}
	fillRect(dc, l.tableHeader, col.panel3)
	headers := []string{"质量分", "代币", "安全", "流动性", "24H成交", "池龄", "24H"}
	fracs := []float64{0.07, 0.29, 0.14, 0.13, 0.14, 0.11, 0.12}
	x := l.tableHeader.Left
	for i, h := range headers {
		cw := int32(float64(width(l.tableHeader)) * fracs[i])
		text(dc, h, rect(x, l.tableHeader.Top, cw, height(l.tableHeader)), fnts.table, col.muted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		x += cw
	}
	if len(app.results) == 0 {
		drawEmpty(dc, l.table)
	} else {
		drawRows(dc, l)
	}
	drawListScrollbar(dc, l, filteredCount)
	line(dc, l.pageFooter.Left, l.pageFooter.Top-s(4), l.pageFooter.Right, l.pageFooter.Top-s(4), col.border)
	text(dc, fmt.Sprintf("显示 %d–%d / %d · 列表内滚动代币，右侧或空白处滚动整页", startNo, endNo, filteredCount), rect(l.pageFooter.Left+s(4), l.pageFooter.Top, s(560), height(l.pageFooter)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	drawButton(dc, idPrevPage, l.buttons[idPrevPage], "向上翻页", app.listOffset > 0, false)
	drawButton(dc, idNextPage, l.buttons[idNextPage], "向下翻页", app.listOffset+radarPageSize(l) < filteredCount, false)
	drawDetails(dc, l.detail)
	drawLogs(dc, l.logs)
	// status bar
	fillRect(dc, l.statusbar, rgb(5, 12, 23))
	line(dc, 0, l.statusbar.Top, l.statusbar.Right, l.statusbar.Top, col.border)
	sd := col.green
	if app.scanning {
		sd = col.yellow
	}
	if app.statusKind == 3 {
		sd = col.red
	}
	ellipse(dc, rect(s(8), l.statusbar.Top+s(9), s(8), s(8)), sd, sd)
	text(dc, statusBarText(), rect(s(24), l.statusbar.Top, s(900), height(l.statusbar)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	text(dc, fmt.Sprintf("整页可下拉 · 实时监控：%s · 界面 %d%%", onOff(app.autoRefresh), []int{100, 115, 130, 150}[app.scaleIndex]), rect(l.statusbar.Right-s(390), l.statusbar.Top, s(375), height(l.statusbar)), fnts.small, col.muted, DT_RIGHT|DT_VCENTER|DT_SINGLELINE)
	drawPageScrollbar(dc, l)
	// Toast
	if app.toast != "" && time.Now().Before(app.toastUntil) {
		tr := rect(l.client.Right-s(380), l.client.Bottom-s(88), s(350), s(48))
		roundRect(dc, tr, col.panel3, col.cyan, s(8))
		text(dc, app.toast, inset(tr, s(10)), fnts.small, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	}
	if app.modal != 0 {
		drawModal(dc, l)
	}
}

func drawEmpty(dc HDC, panel RECT) {
	cx := panel.Left + width(panel)/2
	cy := panel.Top + height(panel)/2
	ellipse(dc, rect(cx-s(28), cy-s(58), s(56), s(56)), rgb(10, 31, 49), col.cyan)
	text(dc, "—", rect(cx-s(28), cy-s(58), s(56), s(56)), fnts.section, col.cyan, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	text(dc, "还没有候选数据", rect(cx-s(200), cy+s(8), s(400), s(28)), fnts.section, col.text, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	text(dc, "第 1 步：先点“演示数据”熟悉界面  ·  第 2 步：点击“立即扫描”并等待 20–60 秒", rect(cx-s(360), cy+s(40), s(720), s(28)), fnts.small, col.muted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
}

func drawRows(dc HDC, l layout) {
	rowH := radarRowHeight()
	y := l.tableHeader.Bottom + s(4)
	visible, _, _ := visibleResultIndices(l)
	fracs := []float64{0.07, 0.29, 0.14, 0.13, 0.14, 0.11, 0.12}
	for row, resultIdx := range visible {
		if resultIdx < 0 || resultIdx >= len(app.results) {
			continue
		}
		t := app.results[resultIdx]
		rr := rect(l.tableHeader.Left, y, width(l.tableHeader), rowH-s(2))
		if resultIdx == app.selected {
			fillRect(dc, rr, rgb(19, 50, 77))
		} else if app.hover == idRowBase+row {
			fillRect(dc, rr, rgb(17, 39, 65))
		}
		line(dc, rr.Left, rr.Bottom, rr.Right, rr.Bottom, rgb(25, 47, 74))
		vals := []string{strconv.Itoa(t.Score), fmt.Sprintf("[%s] %s  %s", chainLabel(t.Chain), t.Symbol, t.Name), t.Security, money(t.Liquidity), money(t.Volume24), ageText(t.AgeHours), fmt.Sprintf("%+.1f%%", t.Change24)}
		x := rr.Left
		for j, v := range vals {
			cw := int32(float64(width(rr)) * fracs[j])
			tc := col.text
			if j == 0 {
				if t.Score >= 80 {
					tc = col.green
				} else if t.Score < 50 {
					tc = col.red
				} else {
					tc = col.yellow
				}
			}
			if j == 6 {
				if t.Change24 >= 0 {
					tc = col.green
				} else {
					tc = col.red
				}
			}
			align := uint32(DT_CENTER)
			if j == 1 {
				align = DT_LEFT
			}
			text(dc, v, rect(x+s(6), rr.Top, cw-s(12), s(43)), fnts.table, tc, align|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
			x += cw
		}
		chartR, chainR := radarRowLinkRects(l, row)
		text(dc, "合约 "+shortAddr(t.Address), rect(rr.Left+s(12), rr.Top+s(42), width(rr)-s(250), s(22)), fnts.small, col.dim, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		text(dc, "走势 ↗", chartR, fnts.small, col.cyan, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		text(dc, "合约页 ↗", chainR, fnts.small, col.cyan, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		y += rowH
	}
}

func radarRowLinkRects(l layout, row int) (RECT, RECT) {
	y := l.tableHeader.Bottom + s(4) + int32(row)*radarRowHeight() + s(42)
	chainR := rect(l.tableHeader.Right-s(104), y, s(94), s(22))
	chartR := rect(chainR.Left-s(86), y, s(78), s(22))
	return chartR, chainR
}

func drawListScrollbar(dc HDC, l layout, total int) {
	pageSize := radarPageSize(l)
	if total <= pageSize || total <= 0 {
		return
	}
	track := rect(l.tableHeader.Right-s(7), l.tableHeader.Bottom+s(6), s(4), l.pageFooter.Top-l.tableHeader.Bottom-s(14))
	roundRect(dc, track, col.panel3, col.panel3, s(2))
	thumbH := int32(float64(height(track)) * float64(pageSize) / float64(total))
	if thumbH < s(36) {
		thumbH = s(36)
	}
	travel := height(track) - thumbH
	maxOffset := total - pageSize
	thumbY := track.Top
	if maxOffset > 0 {
		thumbY += int32(float64(travel) * float64(app.listOffset) / float64(maxOffset))
	}
	roundRect(dc, rect(track.Left, thumbY, width(track), thumbH), col.cyan2, col.cyan2, s(2))
}

func drawPageScrollbar(dc HDC, l layout) {
	if app.modal != 0 || pageScrollRange() <= 0 {
		return
	}
	track := rect(l.client.Right-s(8), s(12), s(4), l.statusbar.Top-s(24))
	if height(track) <= 0 {
		return
	}
	roundRect(dc, track, rgb(19, 38, 61), rgb(19, 38, 61), s(2))
	virtualH := height(l.client) + pageScrollRange()
	thumbH := int32(float64(height(track)) * float64(height(l.client)) / float64(virtualH))
	if thumbH < s(48) {
		thumbH = s(48)
	}
	if thumbH > height(track) {
		thumbH = height(track)
	}
	travel := height(track) - thumbH
	thumbY := track.Top
	if maxScroll := pageScrollRange(); maxScroll > 0 {
		thumbY += int32(float64(travel) * float64(app.pageScroll) / float64(maxScroll))
	}
	roundRect(dc, rect(track.Left, thumbY, width(track), thumbH), col.cyan, col.cyan, s(2))
}

func drawDetails(dc HDC, r RECT) {
	text(dc, "候选详情", rect(r.Left+s(16), r.Top+s(10), s(120), s(30)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	watchLabel := "加入关注"
	if app.selected >= 0 && app.selected < len(app.results) && app.monitor != nil && app.monitor.IsWatching(app.results[app.selected]) {
		watchLabel = "取消关注"
	}
	drawButton(dc, idWatchToggle, buildLayout().buttons[idWatchToggle], watchLabel, app.selected >= 0 && app.selected < len(app.results), watchLabel == "取消关注")
	drawButton(dc, idExpandDetail, buildLayout().buttons[idExpandDetail], "展开", app.selected >= 0 && app.selected < len(app.results), false)
	line(dc, r.Left+s(14), r.Top+s(48), r.Right-s(14), r.Top+s(48), col.border)
	body := rect(r.Left+s(10), r.Top+s(52), width(r)-s(20), height(r)-s(62))
	withClip(dc, body, func() {
		if app.selected < 0 || app.selected >= len(app.results) {
			text(dc, "选择左侧任意代币", rect(r.Left+s(20), r.Top+s(80), width(r)-s(40), s(28)), fnts.body, col.text, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
			text(dc, "这里会显示评分依据、风险证据、合约地址和市场指标。", rect(r.Left+s(34), r.Top+s(116), width(r)-s(68), s(58)), fnts.small, col.muted, DT_CENTER|DT_WORDBREAK)
			return
		}

		t := app.results[app.selected]
		y := body.Top + s(8)
		text(dc, fmt.Sprintf("[%s] %s  %s", chainLabel(t.Chain), t.Symbol, t.Name), rect(body.Left+s(8), y, width(body)-s(16), s(30)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		y += s(36)
		drawButton(dc, idDetailChart, buildLayout().buttons[idDetailChart], "查看走势图 ↗", true, true)
		drawButton(dc, idDetailChain, buildLayout().buttons[idDetailChain], "区块浏览器 ↗", true, false)
		y += s(42)

		scoreR := rect(body.Left+s(8), y, s(72), s(54))
		sc := col.yellow
		if t.Score >= 80 {
			sc = col.green
		} else if t.Score < 50 {
			sc = col.red
		}
		roundRect(dc, scoreR, rgb(9, 29, 48), sc, s(8))
		text(dc, strconv.Itoa(t.Score), scoreR, fnts.number, sc, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		text(dc, t.Grade, rect(scoreR.Right+s(12), y, width(body)-s(104), s(25)), fnts.body, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		text(dc, t.Security, rect(scoreR.Right+s(12), y+s(27), width(body)-s(104), s(23)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		y += s(64)

		leftX := body.Left + s(10)
		colGap := s(12)
		colW := (width(body) - s(20) - colGap) / 2
		rightX := leftX + colW + colGap
		blockText := "发现区块：—"
		if t.BlockNumber > 0 {
			blockText = fmt.Sprintf("发现区块：%d", t.BlockNumber)
		}
		metrics := [][2]string{
			{fmt.Sprintf("链：%s", chainLabel(t.Chain)), fmt.Sprintf("价格：%s", price(t.Price))},
			{fmt.Sprintf("流动性：%s", money(t.Liquidity)), fmt.Sprintf("24H成交：%s", money(t.Volume24))},
			{fmt.Sprintf("池龄：%s", ageText(t.AgeHours)), fmt.Sprintf("24H涨跌：%+.1f%%", t.Change24)},
			{fmt.Sprintf("买/卖：%d / %d", t.Buys24, t.Sells24), fmt.Sprintf("来源：%s", t.Source)},
			{"数据更新：" + dataFreshness(t.UpdatedAt), blockText},
		}
		if t.TaxKnown {
			metrics = append(metrics, [2]string{fmt.Sprintf("买入税：%.1f%%", t.BuyTaxPct), fmt.Sprintf("卖出税：%.1f%%", t.SellTaxPct)})
		} else {
			metrics = append(metrics, [2]string{"买卖税：未确认", "自动策略不会开仓"})
		}
		footerH := s(48)
		footerTop := body.Bottom - footerH
		for _, pair := range metrics {
			if y+s(21) > footerTop-s(6) {
				break
			}
			text(dc, pair[0], rect(leftX, y, colW, s(21)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
			if pair[1] != "" {
				text(dc, pair[1], rect(rightX, y, colW, s(21)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
			}
			y += s(22)
		}

		y += s(4)
		if y+s(55) <= footerTop {
			text(dc, "主要证据", rect(body.Left+s(8), y, width(body)-s(16), s(26)), fnts.body, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
			y += s(29)
			lineH := s(30)
			available := footerTop - y - s(4)
			maxEvidence := 0
			if available > 0 {
				maxEvidence = int(available / lineH)
			}
			if maxEvidence > 4 {
				maxEvidence = 4
			}
			if maxEvidence > len(t.Evidence) {
				maxEvidence = len(t.Evidence)
			}
			for i := 0; i < maxEvidence; i++ {
				text(dc, "• "+t.Evidence[i], rect(body.Left+s(10), y, width(body)-s(20), lineH), fnts.small, col.muted, DT_LEFT|DT_WORDBREAK|DT_END_ELLIPSIS)
				y += lineH
			}
			if len(t.Evidence) > maxEvidence && y+s(22) <= footerTop {
				text(dc, fmt.Sprintf("… 另有 %d 条证据，请点“展开”查看", len(t.Evidence)-maxEvidence), rect(body.Left+s(10), y, width(body)-s(20), s(22)), fnts.small, col.dim, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
			}
		} else if y+s(22) <= footerTop {
			text(dc, "完整证据和池地址请点右上角“展开”", rect(body.Left+s(8), y, width(body)-s(16), s(22)), fnts.small, col.dim, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
		}

		fillRect(dc, rect(body.Left, footerTop-s(1), width(body), s(1)), col.border)
		text(dc, "合约："+shortAddr(t.Address), rect(body.Left+s(8), footerTop+s(2), width(body)-s(16), s(21)), fnts.small, col.dim, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		text(dc, "完整合约与池地址请点“展开”", rect(body.Left+s(8), footerTop+s(23), width(body)-s(16), s(21)), fnts.small, col.dim, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	})
}

func drawLogs(dc HDC, r RECT) {
	text(dc, "运行日志", rect(r.Left+s(16), r.Top+s(10), width(r)-s(32), s(30)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
	drawButton(dc, idMonitorCSV, buildLayout().buttons[idMonitorCSV], "监控报告", app.monitor != nil && app.monitor.Count() > 0, false)
	line(dc, r.Left+s(14), r.Top+s(48), r.Right-s(14), r.Top+s(48), col.border)
	body := rect(r.Left+s(8), r.Top+s(52), width(r)-s(16), height(r)-s(60))
	withClip(dc, body, func() {
		y := body.Top + s(4)
		lineH := s(24)
		maxLines := int((body.Bottom - y) / lineH)
		if maxLines < 0 {
			maxLines = 0
		}
		start := 0
		if len(app.logs) > maxLines {
			start = len(app.logs) - maxLines
		}
		for _, ln := range app.logs[start:] {
			text(dc, ln, rect(body.Left+s(8), y, width(body)-s(16), s(22)), fnts.small, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
			y += lineH
		}
	})
}

func drawModal(dc HDC, l layout) {
	// Fully buffered modal. No multi-step tutorial and no first-run interruption.
	fillRect(dc, l.client, rgb(3, 8, 16))
	r := l.modal
	roundRect(dc, r, col.panel2, col.border2, s(12))
	text(dc, "×", l.buttons[idModalClose], fnts.section, col.muted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	if app.modal == 3 {
		drawExpandedDetails(dc, l)
		return
	}
	if app.modal == 1 {
		text(dc, "程序说明", rect(r.Left+s(30), r.Top+s(24), width(r)-s(100), s(42)), fnts.title, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
		body := "怎么使用\n" +
			"1. 程序默认每 5 秒增量检查 Base、BSC、Arbitrum 新区块；也可以点‘立即扫描’手动刷新。\n" +
			"2. 每个候选最右侧都有‘走势图’链接；详情页可打开走势图和区块浏览器。\n" +
			"3. 切换到‘模拟盘’，查看持仓、净盈亏、止损止盈和交易记录。\n" +
			"4. 在候选列表内滚动可逐行浏览代币；在右侧或空白处滚动可下拉整个页面；点击‘展开’查看完整详情。\n" +
			"5. 点击‘策略档位’可切换：保守、标准、测试、探索；V2.11 默认探索档，用 ≤1 USDC 生成纸面样本。\n\n" +
			"四种策略档位\n" +
			"保守：80分、5万美元流动性、观察3分钟；标准：70分、2.5万美元、观察2分钟；测试：55分、1万美元、观察1分钟；探索：15分、5千美元、观察约30秒、每笔最多1 USDC。探索与影子样本只用于研究，不代表更安全或更赚钱。严重合约风险在任何档位都禁止开仓。\n\n" +
			"评分与模拟的区别\n" +
			"质量分只是研究优先级，不是买入信号。模拟盘会估算 DEX 手续费、Gas、税费和滑点，但无法完全复现实盘的 MEV、报价延迟和无法卖出。\n\n" +
			"退出规则\n" +
			"初始 100 USDC，动态仓位最高 5 USDC，最多 3 个持仓；亏损 8% 止损，盈利 12% 卖一半，盈利 20% 清仓，从最高点回撤 8% 退出。连续亏损 3 笔暂停 3 小时。\n\n" +
			"盈利验证\n" +
			"只按完整自动持仓统计。至少 30 笔、扣除成本后净收益为正、利润因子不低于 1.20、最大回撤不高于 12%，才显示纸面验证通过。\n\n" +
			"注意\n" +
			"程序不连接钱包、不读取助记词、不发送真实交易。模拟盈利不代表实盘可以盈利。"
		text(dc, body, rect(r.Left+s(34), r.Top+s(88), width(r)-s(68), height(r)-s(178)), fnts.body, col.muted, DT_LEFT|DT_WORDBREAK)
		drawButton(dc, idModalNext, l.buttons[idModalNext], "关闭", true, true)
	} else {
		text(dc, "连接诊断", rect(r.Left+s(30), r.Top+s(24), width(r)-s(100), s(42)), fnts.title, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
		text(dc, app.diagText, rect(r.Left+s(30), r.Top+s(90), width(r)-s(60), height(r)-s(160)), fnts.body, col.muted, DT_LEFT|DT_WORDBREAK)
		drawButton(dc, idModalNext, l.buttons[idModalNext], "关闭", true, true)
	}
}

func expandedDetailBody(l layout) RECT {
	return rect(l.modal.Left+s(30), l.modal.Top+s(84), width(l.modal)-s(60), height(l.modal)-s(108))
}

func expandedDetailContentHeight(t Token) int32 {
	return s(430) + int32(len(t.Evidence))*s(48)
}

func clampDetailScroll(l layout) {
	if app.detailScroll < 0 {
		app.detailScroll = 0
	}
	if app.selected < 0 || app.selected >= len(app.results) {
		app.detailScroll = 0
		return
	}
	maxScroll := expandedDetailContentHeight(app.results[app.selected]) - height(expandedDetailBody(l))
	if maxScroll < 0 {
		maxScroll = 0
	}
	if app.detailScroll > maxScroll {
		app.detailScroll = maxScroll
	}
}

func drawExpandedDetails(dc HDC, l layout) {
	if app.selected < 0 || app.selected >= len(app.results) {
		text(dc, "没有选中的代币", rect(l.modal.Left+s(30), l.modal.Top+s(30), width(l.modal)-s(100), s(40)), fnts.title, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
		return
	}
	t := app.results[app.selected]
	text(dc, fmt.Sprintf("[%s] %s · 完整详情", chainLabel(t.Chain), t.Symbol), rect(l.modal.Left+s(30), l.modal.Top+s(20), width(l.modal)-s(390), s(42)), fnts.title, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	drawButton(dc, idModalChart, l.buttons[idModalChart], "走势图 ↗", true, true)
	drawButton(dc, idModalChain, l.buttons[idModalChain], "区块浏览器 ↗", true, false)
	line(dc, l.modal.Left+s(24), l.modal.Top+s(70), l.modal.Right-s(24), l.modal.Top+s(70), col.border)
	body := expandedDetailBody(l)
	clampDetailScroll(l)
	y := body.Top - app.detailScroll
	withClip(dc, body, func() {
		text(dc, t.Name+"  "+t.Symbol, rect(body.Left, y, width(body)-s(20), s(38)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		y += s(42)
		chartURL := selectedDEXURL(t)
		chainURL := explorerTokenURL(t.Chain, t.Address)
		text(dc, "走势图链接："+chartURL, rect(body.Left, y, width(body)-s(30), s(42)), fnts.small, col.cyan, DT_LEFT|DT_WORDBREAK|DT_NOPREFIX)
		y += s(46)
		text(dc, "区块浏览器："+chainURL, rect(body.Left, y, width(body)-s(30), s(42)), fnts.small, col.cyan, DT_LEFT|DT_WORDBREAK|DT_NOPREFIX)
		y += s(52)

		left := body.Left
		colGap := s(24)
		colW := (width(body) - colGap - s(20)) / 2
		right := left + colW + colGap
		metrics := [][2]string{
			{fmt.Sprintf("质量分：%d · %s", t.Score, t.Grade), "安全状态：" + t.Security},
			{"价格：" + price(t.Price), "流动性：" + money(t.Liquidity)},
			{"24H成交：" + money(t.Volume24), fmt.Sprintf("24H涨跌：%+.1f%%", t.Change24)},
			{"池龄：" + ageText(t.AgeHours), fmt.Sprintf("买/卖：%d / %d", t.Buys24, t.Sells24)},
			{"数据更新：" + dataFreshness(t.UpdatedAt), "数据来源：" + t.Source},
		}
		for _, pair := range metrics {
			text(dc, pair[0], rect(left, y, colW, s(26)), fnts.body, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
			text(dc, pair[1], rect(right, y, colW, s(26)), fnts.body, col.muted, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
			y += s(30)
		}
		y += s(8)
		text(dc, "代币合约："+t.Address, rect(body.Left, y, width(body)-s(30), s(30)), fnts.small, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		y += s(34)
		pool := t.PoolAddress
		if pool == "" {
			pool = "尚未获得池地址"
		}
		text(dc, "交易池："+pool, rect(body.Left, y, width(body)-s(30), s(30)), fnts.small, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
		y += s(42)
		text(dc, fmt.Sprintf("全部证据（%d 条）", len(t.Evidence)), rect(body.Left, y, width(body)-s(30), s(30)), fnts.section, col.text, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
		y += s(36)
		for _, evidence := range t.Evidence {
			text(dc, "• "+evidence, rect(body.Left+s(6), y, width(body)-s(42), s(44)), fnts.small, col.muted, DT_LEFT|DT_WORDBREAK|DT_NOPREFIX)
			y += s(48)
		}
	})

	contentH := expandedDetailContentHeight(t)
	if contentH > height(body) {
		track := rect(body.Right-s(8), body.Top, s(4), height(body))
		roundRect(dc, track, col.panel3, col.panel3, s(2))
		thumbH := int32(float64(height(track)) * float64(height(body)) / float64(contentH))
		if thumbH < s(44) {
			thumbH = s(44)
		}
		maxScroll := contentH - height(body)
		thumbY := track.Top
		if maxScroll > 0 {
			thumbY += int32(float64(height(track)-thumbH) * float64(app.detailScroll) / float64(maxScroll))
		}
		roundRect(dc, rect(track.Left, thumbY, width(track), thumbH), col.cyan2, col.cyan2, s(2))
	}
	text(dc, "鼠标滚轮上下查看全部内容", rect(l.modal.Left+s(30), l.modal.Bottom-s(24), width(l.modal)-s(60), s(18)), fnts.small, col.dim, DT_LEFT|DT_VCENTER|DT_SINGLELINE)
}

// ----------------------------- Events and message loop -----------------------------

func wndProc(hwnd HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_CREATE:
		app.hwnd = hwnd
		updateDPI()
		pSetTimer.Call(uintptr(hwnd), 1, 500, 0)
		pSetTimer.Call(uintptr(hwnd), 2, 1000, 0)
		loadCache()
		app.candidatePool = len(loadChainCandidates().Candidates)
		loadSimState()
		loadPreferences()
		app.monitor = loadMonitorState()
		addLog("V2.11 已启动：探索样本、影子研究、拒绝原因统计和严格验证分离")
		addLog("Windows 网络设置：" + windowsProxySummary())
		startUpdateCheck(false)
		return 0
	case WM_ERASEBKGND:
		// We paint the entire client from the back buffer. Returning nonzero prevents
		// DefWindowProc from erasing the background first, which caused visible flashes.
		return 1
	case WM_PAINT:
		var ps PAINTSTRUCT
		hdc, _, _ := pBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
		var cr RECT
		pGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
		w, h := width(cr), height(cr)
		if w > 0 && h > 0 {
			ensureBackBuffer(HDC(hdc), w, h)
			if app.dirty {
				drawUI(app.bb.dc, buildLayout())
				app.dirty = false
			}
			pBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), uintptr(app.bb.dc), 0, 0, SRCCOPY)
		}
		pEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
		return 0
	case WM_SIZE:
		invalidate(false)
		return 0
	case WM_DPICHANGED:
		app.dpi = int32(wParam & 0xffff)
		fontKey = ""
		invalidate(false)
		return 0
	case WM_MOUSEMOVE:
		x, y := loword(lParam), hiword(lParam)
		if !app.mouseTracking {
			t := TRACKMOUSEEVENT{CbSize: uint32(unsafe.Sizeof(TRACKMOUSEEVENT{})), DwFlags: TME_LEAVE, HwndTrack: hwnd}
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&t)))
			app.mouseTracking = true
		}
		nh := hitTest(x, y)
		if nh != app.hover {
			app.hover = nh
		}
		return 0
	case WM_MOUSELEAVE:
		app.mouseTracking = false
		if app.hover != idNone {
			app.hover = idNone
		}
		return 0
	case WM_LBUTTONDOWN:
		x, y := loword(lParam), hiword(lParam)
		np := hitTest(x, y)
		if app.searchFocused && np != idSearchBox && np != idClearSearch {
			app.searchFocused = false
		}
		if np != app.pressed {
			app.pressed = np
			invalidate(false)
		}
		return 0
	case WM_LBUTTONUP:
		x, y := loword(lParam), hiword(lParam)
		id := hitTest(x, y)
		pressed := app.pressed
		app.pressed = idNone
		if id == pressed && id != idNone {
			handleClick(id)
		} else {
			invalidate(false)
		}
		return 0
	case WM_SETCURSOR:
		if app.hover != idNone {
			cur, _, _ := pLoadCursorW.Call(0, IDC_HAND)
			pSetCursor.Call(cur)
			return 1
		}
		cur, _, _ := pLoadCursorW.Call(0, IDC_ARROW)
		pSetCursor.Call(cur)
		return 1
	case WM_KEYDOWN:
		if wParam == VK_ESCAPE {
			if app.modal != 0 {
				app.modal = 0
				invalidate(false)
				return 0
			}
			if app.searchFocused {
				app.searchFocused = false
				invalidate(false)
				return 0
			}
		}
	case WM_CHAR:
		if app.searchFocused && app.modal == 0 && app.page == 0 {
			r := rune(wParam)
			switch r {
			case 8:
				runes := []rune(app.searchText)
				if len(runes) > 0 {
					app.searchText = string(runes[:len(runes)-1])
				}
			case 13, 27:
				app.searchFocused = false
			default:
				if r >= 32 && len([]rune(app.searchText)) < 64 {
					app.searchText += string(r)
				}
			}
			resetListPosition()
			selectFirstVisible(buildLayout())
			invalidate(false)
			return 0
		}
	case WM_MOUSEWHEEL:
		delta := int16(uint16((wParam >> 16) & 0xffff))
		if app.modal == 3 {
			step := s(64)
			if delta > 0 {
				app.detailScroll -= step
			} else if delta < 0 {
				app.detailScroll += step
			}
			clampDetailScroll(buildLayout())
			invalidate(false)
			return 0
		}
		if app.modal == 0 {
			l := buildLayout()
			pt := POINT{
				X: int32(int16(uint16(lParam & 0xffff))),
				Y: int32(int16(uint16((lParam >> 16) & 0xffff))),
			}
			pScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
			if app.page == 0 && contains(l.table, pt.X, pt.Y) {
				rows := 3
				if delta > 0 {
					rows = -3
				}
				changed := scrollListRows(l, rows)
				if changed {
					selectFirstVisible(l)
				}
			} else {
				step := s(88)
				if delta > 0 {
					step = -step
				}
				scrollPage(step)
			}
			invalidate(false)
			return 0
		}
	case WM_TIMER:
		if wParam == 1 {
			if app.toast != "" && time.Now().After(app.toastUntil) {
				app.toast = ""
				invalidate(false)
			}
		} else if wParam == 2 {
			if app.autoRefresh && !app.scanning && time.Since(app.lastPollTry) >= 5*time.Second {
				app.lastPollTry = time.Now()
				startScan(false)
			}
			if app.page == 0 && app.modal == 0 {
				invalidate(false)
			}
			if !app.updateChecking && time.Since(app.lastUpdateCheck) >= 6*time.Hour {
				startUpdateCheck(false)
			}
		}
		return 0
	case WM_SCAN_DONE:
		finishScan()
		return 0
	case WM_DIAG_DONE:
		finishDiagnostic()
		return 0
	case WM_UPDATE_DONE:
		finishUpdateTask()
		return 0
	case WM_CLOSE:
		if app.scanCancel != nil {
			app.scanCancel()
		}
		pKillTimer.Call(uintptr(hwnd), 1)
		pKillTimer.Call(uintptr(hwnd), 2)
		pDestroyWindow.Call(uintptr(hwnd))
		return 0
	case WM_DESTROY:
		cleanup()
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func updateDPI() {
	if app.hwnd != 0 {
		d, _, _ := pGetDpiForWindow.Call(uintptr(app.hwnd))
		if d >= 96 {
			app.dpi = int32(d)
		}
	}
}
func invalidate(erase bool) {
	app.dirty = true
	e := uintptr(0)
	if erase {
		e = 1
	}
	pInvalidateRect.Call(uintptr(app.hwnd), 0, e)
}

func hitTest(x, y int32) int {
	l := buildLayout()
	if app.modal != 0 {
		ids := []int{idModalClose, idModalNext}
		if app.modal == 3 {
			ids = []int{idModalClose, idModalChart, idModalChain}
		}
		for _, id := range ids {
			if contains(l.buttons[id], x, y) {
				return id
			}
		}
		return idNone
	}
	for _, id := range []int{idTutorial, idUpdate, idPageRadar, idPageSim} {
		if contains(l.buttons[id], x, y) {
			return id
		}
	}
	if app.page == 0 {
		for _, id := range []int{idScan, idStop, idDemo, idExport, idOpenDEX, idTutorial, idDiagnose, idScaleDown, idScaleUp, idAuto, idSimBuy, idFilterAll, idFilterWatch, idFilterSafe, idFilterWait, idSortMode, idClearSearch, idSearchBox, idPrevPage, idNextPage, idWatchToggle, idMonitorCSV, idDetailChart, idDetailChain, idExpandDetail} {
			if contains(l.buttons[id], x, y) {
				return id
			}
		}
		if len(app.results) > 0 && x >= l.tableHeader.Left && x < l.tableHeader.Right && y >= l.tableHeader.Bottom+s(4) && y < l.pageFooter.Top {
			rowH := radarRowHeight()
			idx := int((y - (l.tableHeader.Bottom + s(4))) / rowH)
			visible, _, _ := visibleResultIndices(l)
			if idx >= 0 && idx < len(visible) {
				chartR, chainR := radarRowLinkRects(l, idx)
				if contains(chainR, x, y) {
					return idChainBase + idx
				}
				if contains(chartR, x, y) {
					return idChartBase + idx
				}
				return idRowBase + idx
			}
		}
	} else {
		for _, id := range []int{idSimSell, idSimCloseAll, idSimExport, idSimReset, idSimProfile, idSimAuto} {
			if contains(l.buttons[id], x, y) {
				return id
			}
		}
		if contains(l.simPositions, x, y) && y >= l.simPosHeader.Bottom {
			rowH := s(44)
			idx := int((y - (l.simPosHeader.Bottom + s(4))) / rowH)
			if idx >= 0 && idx < len(app.sim.Positions) {
				return idPosBase + idx
			}
		}
		if contains(l.simTrades, x, y) && y >= l.simTradeHeader.Bottom {
			rowH := s(42)
			row := int((y - (l.simTradeHeader.Bottom + s(4))) / rowH)
			idx := len(app.sim.Trades) - 1 - row
			if idx >= 0 && idx < len(app.sim.Trades) {
				return idTradeBase + idx
			}
		}
	}
	return idNone
}

func handleClick(id int) {
	switch {
	case id == idScan:
		startScan(true)
	case id == idStop:
		if app.scanCancel != nil {
			app.scanCancel()
			addLog("已请求停止扫描……")
		}
	case id == idDemo:
		loadDemo()
	case id == idExport:
		exportCSV()
	case id == idOpenDEX:
		openSelectedDEX()
	case id == idDetailChart || id == idModalChart:
		openSelectedDEX()
	case id == idDetailChain || id == idModalChain:
		openSelectedExplorer()
	case id == idExpandDetail:
		if app.selected >= 0 && app.selected < len(app.results) {
			app.detailScroll = 0
			app.modal = 3
			invalidate(false)
		}
	case id == idTutorial:
		app.modal = 1
		invalidate(false)
	case id == idUpdate:
		if app.updateInfo.Available {
			startUpdateInstall()
		} else {
			startUpdateCheck(true)
		}
	case id == idDiagnose:
		startDiagnostic()
	case id == idWatchToggle:
		toggleSelectedWatch()
	case id == idMonitorCSV:
		exportMonitorReport()
	case id == idFilterAll:
		app.filterMode = 0
		resetListPosition()
		selectFirstVisible(buildLayout())
		invalidate(false)
	case id == idFilterWatch:
		app.filterMode = 1
		resetListPosition()
		selectFirstVisible(buildLayout())
		invalidate(false)
	case id == idFilterSafe:
		app.filterMode = 2
		resetListPosition()
		selectFirstVisible(buildLayout())
		invalidate(false)
	case id == idFilterWait:
		app.filterMode = 3
		resetListPosition()
		selectFirstVisible(buildLayout())
		invalidate(false)
	case id == idSortMode:
		app.sortMode = (app.sortMode + 1) % 3
		resetListPosition()
		selectFirstVisible(buildLayout())
		invalidate(false)
	case id == idSearchBox:
		app.searchFocused = true
		invalidate(false)
	case id == idClearSearch:
		app.searchText = ""
		resetListPosition()
		app.searchFocused = true
		selectFirstVisible(buildLayout())
		invalidate(false)
	case id == idPrevPage:
		l := buildLayout()
		if scrollListRows(l, -radarPageSize(l)) {
			selectFirstVisible(buildLayout())
			invalidate(false)
		}
	case id == idNextPage:
		l := buildLayout()
		if scrollListRows(l, radarPageSize(l)) {
			selectFirstVisible(l)
			invalidate(false)
		}
	case id == idScaleDown:
		if app.scaleIndex > 0 {
			app.scaleIndex--
			app.uiScale = []float64{1, 1.15, 1.30, 1.50}[app.scaleIndex]
			fontKey = ""
			invalidate(false)
		}
	case id == idScaleUp:
		if app.scaleIndex < 3 {
			app.scaleIndex++
			app.uiScale = []float64{1, 1.15, 1.30, 1.50}[app.scaleIndex]
			fontKey = ""
			invalidate(false)
		}
	case id == idAuto:
		app.autoRefresh = !app.autoRefresh
		app.toast = "5秒实时监控已" + onOff(app.autoRefresh)
		app.toastUntil = time.Now().Add(2 * time.Second)
		if app.autoRefresh {
			app.lastPollTry = time.Time{}
		}
		savePreferences()
		invalidate(false)
	case id == idPageRadar:
		app.page = 0
		app.pageScroll = 0
		invalidate(false)
	case id == idPageSim:
		app.page = 1
		app.pageScroll = 0
		app.selectedPos = -1
		app.selectedTrade = -1
		invalidate(false)
	case id == idSimBuy:
		manualSimBuy()
	case id == idSimSell:
		manualSimSellSelected()
	case id == idSimCloseAll:
		closeAllSimPositions("用户全部平仓")
	case id == idSimProfile:
		name := app.sim.CycleProfile()
		saveSimState()
		app.toast = "自动模拟已切换到" + name + "档"
		app.toastUntil = time.Now().Add(3 * time.Second)
		addLog("模拟策略档位切换为：" + name)
		invalidate(false)
	case id == idSimAuto:
		app.sim.AutoEnabled = !app.sim.AutoEnabled
		saveSimState()
		app.toast = "自动模拟策略已" + onOff(app.sim.AutoEnabled) + "（" + app.sim.ProfileName() + "档）"
		app.toastUntil = time.Now().Add(3 * time.Second)
		invalidate(false)
	case id == idSimReset:
		if time.Now().Before(app.resetConfirmUntil) {
			app.sim.Reset()
			app.selectedPos = -1
			app.selectedTrade = -1
			saveSimState()
			app.resetConfirmUntil = time.Time{}
			app.toast = "模拟账户已重置为 100 USDC"
			addLog("模拟账户已由用户重置")
		} else {
			app.resetConfirmUntil = time.Now().Add(5 * time.Second)
			app.toast = "5秒内再次点击“重置模拟盘”确认清空"
		}
		app.toastUntil = time.Now().Add(5 * time.Second)
		invalidate(false)
	case id == idSimExport:
		exportSimCSV()
	case id == idModalClose:
		app.modal = 0
		invalidate(false)
	case id == idModalPrev:
		// Reserved for compatibility; the multi-step tutorial was removed in V2.2.
	case id == idModalNext:
		app.modal = 0
		invalidate(false)
	case id >= idChainBase:
		row := id - idChainBase
		visible, _, _ := visibleResultIndices(buildLayout())
		if row >= 0 && row < len(visible) {
			app.selected = visible[row]
			app.detailScroll = 0
			openSelectedExplorer()
			invalidate(false)
		}
	case id >= idChartBase:
		row := id - idChartBase
		visible, _, _ := visibleResultIndices(buildLayout())
		if row >= 0 && row < len(visible) {
			app.selected = visible[row]
			app.detailScroll = 0
			openSelectedDEX()
			invalidate(false)
		}
	case id >= idTradeBase:
		idx := id - idTradeBase
		if idx >= 0 && idx < len(app.sim.Trades) {
			app.selectedTrade = idx
			app.selectedPos = -1
			invalidate(false)
		}
	case id >= idPosBase:
		idx := id - idPosBase
		if idx >= 0 && idx < len(app.sim.Positions) {
			app.selectedPos = idx
			app.selectedTrade = -1
			invalidate(false)
		}
	case id >= idRowBase:
		row := id - idRowBase
		visible, _, _ := visibleResultIndices(buildLayout())
		if row >= 0 && row < len(visible) {
			app.selected = visible[row]
			app.detailScroll = 0
			invalidate(false)
		}
	}
}

func cleanup() {
	saveSimState()
	savePreferences()
	_ = saveMonitorState(app.monitor)
	closeNetwork()
	deleteFonts()
	if app.bb.dc != 0 {
		if app.bb.old != 0 {
			pSelectObject.Call(uintptr(app.bb.dc), uintptr(app.bb.old))
		}
		if app.bb.bmp != 0 {
			pDeleteObject.Call(uintptr(app.bb.bmp))
		}
		pDeleteDC.Call(uintptr(app.bb.dc))
	}
}

// ----------------------------- Network and scoring -----------------------------

type scanOutcome struct {
	id     uint64
	tokens []Token
	stats  scanStats
	err    error
	logs   []string
	manual bool
}

var scanOutcomeCh = make(chan scanOutcome, 1)
var diagOutcomeCh = make(chan string, 1)

func startScan(manual bool) {
	if app.scanning {
		return
	}
	// Remove a stale completion left by an interrupted previous run.
	select {
	case <-scanOutcomeCh:
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	app.scanCancel = cancel
	app.scanning = true
	app.scanSeq++
	app.activeScanID = app.scanSeq
	id := app.activeScanID
	if manual {
		app.status = "正在手动扫描"
	} else {
		app.status = "正在实时增量监控"
	}
	app.statusKind = 2
	if manual {
		addLog("开始手动扫描；最长等待 55 秒，超时会自动停止并保留旧结果")
	}
	invalidate(false)
	held := []string{}
	if app.sim != nil {
		for _, p := range app.sim.Positions {
			held = append(held, simKey(p.Chain, p.Address))
		}
	}
	if app.monitor != nil {
		held = append(held, app.monitor.WatchedIdentities()...)
	}
	go func(scanID uint64, heldAddresses []string) {
		tokens, logs, stats, err := multiScanMarket(ctx, heldAddresses)
		out := scanOutcome{id: scanID, tokens: tokens, stats: stats, logs: logs, err: err, manual: manual}
		select {
		case scanOutcomeCh <- out:
			pPostMessageW.Call(uintptr(app.hwnd), WM_SCAN_DONE, 0, 0)
		default:
			// The UI is closing or a newer run superseded this result.
		}
	}(id, held)
}

func finishScan() {
	var out scanOutcome
	select {
	case out = <-scanOutcomeCh:
	default:
		addLog("忽略了一条无结果的扫描完成消息")
		return
	}
	if out.id != app.activeScanID {
		addLog("已忽略过期扫描结果")
		return
	}
	app.scanning = false
	app.scanCancel = nil
	app.lastPollTry = time.Now()
	app.lastScanDuration = out.stats.Duration
	if out.stats.LatestBlock > 0 {
		app.latestBlock = out.stats.LatestBlock
	}
	app.chainSummary = out.stats.ChainSummary
	app.activeChains = out.stats.ActiveChains
	app.candidatePool = out.stats.CandidatePool
	app.lastBatchAnalyzed = out.stats.BatchAnalyzed
	quietHeartbeat := false
	logCompletion := out.manual
	for _, l := range out.logs {
		if strings.Contains(l, "本轮新发现 0 条") {
			quietHeartbeat = true
			break
		}
	}
	if quietHeartbeat && app.autoRefresh && !out.manual && time.Since(app.lastHeartbeatLog) < time.Minute {
		// Keep the UI and disk log quiet during normal 5-second polling.
	} else if quietHeartbeat && app.autoRefresh && !out.manual {
		addLog("实时监控正常：多链区块游标持续推进，当前没有新池事件")
		app.lastHeartbeatLog = time.Now()
	} else {
		for _, l := range out.logs {
			addLog(l)
		}
		logCompletion = true
	}
	if out.err != nil {
		if errors.Is(out.err, context.DeadlineExceeded) {
			app.status = "扫描超时，已自动停止"
			app.statusKind = 2
			addLog("扫描超过 55 秒，已自动停止并保留现有结果")
		} else if errors.Is(out.err, context.Canceled) {
			app.status = "扫描已停止"
			app.statusKind = 2
			addLog("扫描已由用户停止，保留现有结果")
		} else {
			app.status = "多链扫描暂不可用"
			app.statusKind = 3
			addLog("扫描失败：" + out.err.Error())
		}
	} else {
		selectedAddress := ""
		if app.selected >= 0 && app.selected < len(app.results) {
			selectedAddress = tokenIdentity(app.results[app.selected].Chain, app.results[app.selected].Address)
		}
		app.results = out.tokens
		app.selected = -1
		if selectedAddress != "" {
			for i := range app.results {
				if tokenIdentity(app.results[i].Chain, app.results[i].Address) == selectedAddress {
					app.selected = i
					break
				}
			}
		}
		if app.selected < 0 && len(app.results) > 0 {
			app.selected = 0
		}
		app.lastScan = time.Now()
		app.lastSuccess = app.lastScan
		l := buildLayout()
		clampListPage(l)
		selectFirstVisible(l)
		app.status = fmt.Sprintf("多链监控正常 %d/3 · 列表 %d / 候选库 %d", app.activeChains, len(app.results), app.candidatePool)
		app.statusKind = 1
		if app.monitor != nil {
			for _, alert := range app.monitor.Observe(app.results, app.lastScan) {
				addLog("可信告警：" + alert.Symbol + " · " + alert.Message)
			}
			if err := saveMonitorState(app.monitor); err != nil {
				addLog("可信监控保存失败：" + shortErr(err))
			}
		}
		quotes := tokensToQuotes(app.results, app.lastScan)
		app.sim.AddSnapshots(quotes, app.lastScan)
		for _, e := range app.sim.Update(quotes, app.lastScan) {
			addLog("模拟盘：" + e)
		}
		for _, e := range app.sim.UpdateShadows(quotes, app.lastScan) {
			addLog("影子研究：" + e)
		}
		for _, e := range app.sim.AutoEvaluate(quotes, app.lastScan) {
			addLog("模拟盘：" + e)
		}
		saveCache()
		saveSimState()
		if logCompletion {
			addLog(fmt.Sprintf("实时更新完成：列表 %d 个 / 候选库 %d 个 / 本轮分析 %d 个 / 耗时 %.1f 秒", len(app.results), app.candidatePool, app.lastBatchAnalyzed, app.lastScanDuration.Seconds()))
		}
	}
	invalidate(false)
}

func startDiagnostic() {
	if app.scanning {
		return
	}
	app.status = "正在进行连接诊断"
	app.statusKind = 2
	app.modal = 2
	app.diagText = "正在测试 三条链 RPC、DEX Screener 与 GoPlus，请稍候……"
	invalidate(false)
	go func() {
		diagOutcomeCh <- diagnoseMultiChain()
		pPostMessageW.Call(uintptr(app.hwnd), WM_DIAG_DONE, 0, 0)
	}()
}
func finishDiagnostic() {
	app.diagText = <-diagOutcomeCh
	app.status = "连接诊断完成"
	app.statusKind = 1
	addLog("连接诊断已完成")
	invalidate(false)
}

var directTransport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	MaxIdleConns:          24,
	MaxIdleConnsPerHost:   6,
	MaxConnsPerHost:       6,
	IdleConnTimeout:       45 * time.Second,
	TLSHandshakeTimeout:   6 * time.Second,
	ResponseHeaderTimeout: 7 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

var directHTTPClient = &http.Client{Timeout: 9 * time.Second, Transport: directTransport}
var winHTTPSession HINTERNET
var winHTTPOnce sync.Once
var winHTTPSessionErr error

type httpResult struct {
	Status int
	Body   []byte
	Engine string
}

func initWinHTTPSession() (HINTERNET, error) {
	winHTTPOnce.Do(func() {
		h, _, callErr := pWinHttpOpen.Call(
			uintptr(unsafe.Pointer(utf16Ptr("MultiChainTokenRadar/2.9"))),
			WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY,
			0,
			0,
			0,
		)
		if h == 0 {
			winHTTPSessionErr = fmt.Errorf("WinHTTP 初始化失败：%s", formatWinHTTPCallError(callErr))
			return
		}
		winHTTPSession = HINTERNET(h)
		// Resolve, connect, send and receive timeout in milliseconds.
		if ok, _, callErr := pWinHttpSetTimeouts.Call(h, 5000, 6000, 6000, 9000); ok == 0 {
			pWinHttpCloseHandle.Call(h)
			winHTTPSession = 0
			winHTTPSessionErr = fmt.Errorf("WinHTTP 超时设置失败：%s", formatWinHTTPCallError(callErr))
		}
	})
	return winHTTPSession, winHTTPSessionErr
}

func closeNetwork() {
	directTransport.CloseIdleConnections()
	if winHTTPSession != 0 {
		pWinHttpCloseHandle.Call(uintptr(winHTTPSession))
		winHTTPSession = 0
	}
}

func winHTTPGet(ctx context.Context, raw string) (httpResult, error) {
	if err := ctx.Err(); err != nil {
		return httpResult{}, err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return httpResult{}, fmt.Errorf("无效网址：%s", raw)
	}
	if !strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http") {
		return httpResult{}, fmt.Errorf("不支持的网址协议：%s", u.Scheme)
	}
	session, err := initWinHTTPSession()
	if err != nil {
		return httpResult{}, err
	}
	port := 80
	flags := uintptr(0)
	if strings.EqualFold(u.Scheme, "https") {
		port = 443
		flags = WINHTTP_FLAG_SECURE
	}
	if p := u.Port(); p != "" {
		if n, e := strconv.Atoi(p); e == nil && n > 0 && n <= 65535 {
			port = n
		}
	}
	connect, _, callErr := pWinHttpConnect.Call(uintptr(session), uintptr(unsafe.Pointer(utf16Ptr(u.Hostname()))), uintptr(port), 0)
	if connect == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 连接初始化失败：%s", formatWinHTTPCallError(callErr))
	}
	defer pWinHttpCloseHandle.Call(connect)
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	request, _, callErr := pWinHttpOpenRequest.Call(
		connect,
		uintptr(unsafe.Pointer(utf16Ptr("GET"))),
		uintptr(unsafe.Pointer(utf16Ptr(path))),
		0,
		0,
		0,
		flags,
	)
	if request == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 创建请求失败：%s", formatWinHTTPCallError(callErr))
	}
	defer pWinHttpCloseHandle.Call(request)

	headers := "Accept: application/json\r\nAccept-Encoding: identity\r\nCache-Control: no-cache\r\n"
	if ok, _, callErr := pWinHttpAddRequestHeaders.Call(
		request,
		uintptr(unsafe.Pointer(utf16Ptr(headers))),
		uintptr(^uint32(0)),
		WINHTTP_ADDREQ_FLAG_ADD|WINHTTP_ADDREQ_FLAG_REPLACE,
	); ok == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 设置请求头失败：%s", formatWinHTTPCallError(callErr))
	}
	if err := ctx.Err(); err != nil {
		return httpResult{}, err
	}
	if ok, _, callErr := pWinHttpSendRequest.Call(request, 0, 0, 0, 0, 0, 0); ok == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 发送失败：%s", formatWinHTTPCallError(callErr))
	}
	if ok, _, callErr := pWinHttpReceiveResponse.Call(request, 0); ok == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 接收失败：%s", formatWinHTTPCallError(callErr))
	}
	var status uint32
	sz := uint32(unsafe.Sizeof(status))
	if ok, _, callErr := pWinHttpQueryHeaders.Call(
		request,
		WINHTTP_QUERY_STATUS_CODE|WINHTTP_QUERY_FLAG_NUMBER,
		0,
		uintptr(unsafe.Pointer(&status)),
		uintptr(unsafe.Pointer(&sz)),
		0,
	); ok == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 读取状态码失败：%s", formatWinHTTPCallError(callErr))
	}
	body := make([]byte, 0, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return httpResult{}, err
		}
		var available uint32
		if ok, _, callErr := pWinHttpQueryDataAvailable.Call(request, uintptr(unsafe.Pointer(&available))); ok == 0 {
			return httpResult{}, fmt.Errorf("WinHTTP 查询响应失败：%s", formatWinHTTPCallError(callErr))
		}
		if available == 0 {
			break
		}
		if len(body)+int(available) > 8<<20 {
			return httpResult{}, errors.New("接口响应超过 8MB，已终止")
		}
		chunk := make([]byte, available)
		var read uint32
		if ok, _, callErr := pWinHttpReadData.Call(request, uintptr(unsafe.Pointer(&chunk[0])), uintptr(available), uintptr(unsafe.Pointer(&read))); ok == 0 {
			return httpResult{}, fmt.Errorf("WinHTTP 读取响应失败：%s", formatWinHTTPCallError(callErr))
		}
		if read == 0 {
			break
		}
		body = append(body, chunk[:read]...)
	}
	return httpResult{Status: int(status), Body: body, Engine: "Windows 自动代理"}, nil
}

func directHTTPGet(ctx context.Context, raw string) (httpResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return httpResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "MultiChainTokenRadar/2.5")
	resp, err := directHTTPClient.Do(req)
	if err != nil {
		return httpResult{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{Status: resp.StatusCode, Body: b, Engine: "Go 直连/环境代理"}, nil
}

func fetchURL(ctx context.Context, raw string) (httpResult, error) {
	// Primary path: WinHTTP automatic proxy. This follows the user's Windows and
	// per-user proxy configuration, including the proxy set by Clash-like clients.
	wr, werr := winHTTPGet(ctx, raw)
	if werr == nil {
		return wr, nil
	}
	if err := ctx.Err(); err != nil {
		return httpResult{}, err
	}
	// Secondary path: direct Go HTTP or HTTP(S)_PROXY environment variables.
	dr, derr := directHTTPGet(ctx, raw)
	if derr == nil {
		return dr, nil
	}
	return httpResult{}, fmt.Errorf("Windows 自动代理失败：%v；直连也失败：%v", werr, derr)
}

func getJSON(ctx context.Context, raw string, v any) error {
	res, err := fetchURL(ctx, raw)
	if err != nil {
		return err
	}
	if res.Status < 200 || res.Status >= 300 {
		body := strings.TrimSpace(string(res.Body))
		if len([]rune(body)) > 300 {
			body = string([]rune(body)[:300]) + "…"
		}
		return fmt.Errorf("HTTP %d（%s）：%s", res.Status, res.Engine, body)
	}
	if len(res.Body) == 0 {
		return fmt.Errorf("接口返回空内容（%s）", res.Engine)
	}
	dec := json.NewDecoder(strings.NewReader(string(res.Body)))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("JSON 解析失败（%s）：%w", res.Engine, err)
	}
	return nil
}

func formatWinHTTPCallError(err error) string {
	if err == nil {
		return "未知错误"
	}
	code := uint32(0)
	if errno, ok := err.(syscall.Errno); ok {
		code = uint32(errno)
	}
	meaning := map[uint32]string{
		12002: "请求超时",
		12005: "网址无效",
		12007: "域名解析失败",
		12017: "操作被取消",
		12029: "无法连接服务器或代理",
		12030: "连接被中断",
		12031: "连接已重置",
		12037: "TLS 证书日期无效",
		12038: "TLS 证书域名不匹配",
		12044: "代理需要身份验证",
		12166: "自动代理检测失败",
		12167: "代理脚本不可用",
		12175: "TLS 安全通道失败",
	}
	if m := meaning[code]; m != "" {
		return fmt.Sprintf("%s（错误码 %d）", m, code)
	}
	return fmt.Sprintf("%v（错误码 %d）", err, code)
}

func utf16PtrString(p *uint16) string {
	if p == nil {
		return ""
	}
	buf := make([]uint16, 0, 256)
	for i := uintptr(0); i < 32768; i++ {
		v := *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + i*2))
		if v == 0 {
			break
		}
		buf = append(buf, v)
	}
	return syscall.UTF16ToString(buf)
}

func windowsProxySummary() string {
	var cfg WINHTTP_CURRENT_USER_IE_PROXY_CONFIG
	ok, _, callErr := pWinHttpGetIEProxyConfigForCurrentUser.Call(uintptr(unsafe.Pointer(&cfg)))
	if ok == 0 {
		return "Windows 代理设置读取失败：" + formatWinHTTPCallError(callErr)
	}
	defer func() {
		if cfg.AutoConfigURL != nil {
			pGlobalFree.Call(uintptr(unsafe.Pointer(cfg.AutoConfigURL)))
		}
		if cfg.Proxy != nil {
			pGlobalFree.Call(uintptr(unsafe.Pointer(cfg.Proxy)))
		}
		if cfg.ProxyBypass != nil {
			pGlobalFree.Call(uintptr(unsafe.Pointer(cfg.ProxyBypass)))
		}
	}()
	parts := []string{}
	if cfg.AutoDetect != 0 {
		parts = append(parts, "自动检测已开启")
	}
	if v := utf16PtrString(cfg.AutoConfigURL); v != "" {
		parts = append(parts, "PAC="+v)
	}
	if v := utf16PtrString(cfg.Proxy); v != "" {
		parts = append(parts, "代理="+v)
	}
	if v := utf16PtrString(cfg.ProxyBypass); v != "" {
		parts = append(parts, "绕过="+v)
	}
	if len(parts) == 0 {
		return "Windows 未配置显式代理；程序会尝试直连"
	}
	return strings.Join(parts, "；")
}

// ----------------------------- Base JSON-RPC -----------------------------

const (
	baseRPCPrimary   = "https://mainnet.base.org"
	baseRPCSecondary = "https://mainnet-preconf.base.org"

	aerodromeFactory = "0x420dd381b31aef6683db6b902084cb0ffece40da"
	uniswapV3Factory = "0x33128a8fc17869897dce68ed026d694621f6fdfd"
	uniswapV2Factory = "0x8909dc15e40173ff4699343b6eb8132c65e18ec6"

	topicAerodromePoolCreated = "0x2128d88d14c80cb081c1252a5acff7a264671bf199ce226b53788fb26065005e"
	topicUniswapV3PoolCreated = "0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118"
	topicUniswapV2PairCreated = "0x0d3648bd0f6ba80134a33ba9275ac585d9d315f0ad8355cddefde31afa28d0e9"
)

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type rpcLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	BlockNumber     string   `json:"blockNumber"`
	TransactionHash string   `json:"transactionHash"`
	LogIndex        string   `json:"logIndex"`
	Removed         bool     `json:"removed"`
}

type chainCandidate struct {
	TokenAddress string    `json:"token_address"`
	PoolAddress  string    `json:"pool_address"`
	Factory      string    `json:"factory"`
	BlockNumber  uint64    `json:"block_number"`
	SeenAt       time.Time `json:"seen_at"`
}

type chainCandidateFile struct {
	LastBlock  uint64           `json:"last_block"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Candidates []chainCandidate `json:"candidates"`
}

type factorySpec struct {
	Name, Address, Topic, Kind string
}

var baseFactories = []factorySpec{
	{Name: "Aerodrome", Address: aerodromeFactory, Topic: topicAerodromePoolCreated, Kind: "aero"},
	{Name: "Uniswap V3", Address: uniswapV3Factory, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
	{Name: "Uniswap V2", Address: uniswapV2Factory, Topic: topicUniswapV2PairCreated, Kind: "v2"},
}

var knownBaseAssets = map[string]bool{
	"0x4200000000000000000000000000000000000006": true, // WETH
	"0x833589fcd6edb6e08f4c7c32d4f71b54bda02913": true, // native USDC
	"0xd9aa3213c4d10a4f6e06a01d9a62f3ecf8a3f6f2": true, // USDbC
	"0xcbb7c0000ab88b473b1f5afd9ef808440eed33bf": true, // cbBTC
	"0x50c5725949a6f0c72e6c4a641f24049a917db0cb": true, // DAI
	"0x940181a94a35a4569e4529a3cdfb74e38fd98631": true, // AERO
}

func winHTTPPostJSON(ctx context.Context, raw string, body []byte) (httpResult, error) {
	if err := ctx.Err(); err != nil {
		return httpResult{}, err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return httpResult{}, fmt.Errorf("无效网址：%s", raw)
	}
	session, err := initWinHTTPSession()
	if err != nil {
		return httpResult{}, err
	}
	port, flags := 80, uintptr(0)
	if strings.EqualFold(u.Scheme, "https") {
		port, flags = 443, WINHTTP_FLAG_SECURE
	}
	connect, _, callErr := pWinHttpConnect.Call(uintptr(session), uintptr(unsafe.Pointer(utf16Ptr(u.Hostname()))), uintptr(port), 0)
	if connect == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 连接初始化失败：%s", formatWinHTTPCallError(callErr))
	}
	defer pWinHttpCloseHandle.Call(connect)
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	request, _, callErr := pWinHttpOpenRequest.Call(connect, uintptr(unsafe.Pointer(utf16Ptr("POST"))), uintptr(unsafe.Pointer(utf16Ptr(path))), 0, 0, 0, flags)
	if request == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 创建请求失败：%s", formatWinHTTPCallError(callErr))
	}
	defer pWinHttpCloseHandle.Call(request)
	headers := "Accept: application/json\r\nContent-Type: application/json\r\nAccept-Encoding: identity\r\nCache-Control: no-cache\r\n"
	if ok, _, callErr := pWinHttpAddRequestHeaders.Call(request, uintptr(unsafe.Pointer(utf16Ptr(headers))), uintptr(^uint32(0)), WINHTTP_ADDREQ_FLAG_ADD|WINHTTP_ADDREQ_FLAG_REPLACE); ok == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 设置请求头失败：%s", formatWinHTTPCallError(callErr))
	}
	var ptr uintptr
	if len(body) > 0 {
		ptr = uintptr(unsafe.Pointer(&body[0]))
	}
	if ok, _, callErr := pWinHttpSendRequest.Call(request, 0, 0, ptr, uintptr(len(body)), uintptr(len(body)), 0); ok == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 发送失败：%s", formatWinHTTPCallError(callErr))
	}
	if ok, _, callErr := pWinHttpReceiveResponse.Call(request, 0); ok == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 接收失败：%s", formatWinHTTPCallError(callErr))
	}
	var status uint32
	sz := uint32(unsafe.Sizeof(status))
	if ok, _, callErr := pWinHttpQueryHeaders.Call(request, WINHTTP_QUERY_STATUS_CODE|WINHTTP_QUERY_FLAG_NUMBER, 0, uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&sz)), 0); ok == 0 {
		return httpResult{}, fmt.Errorf("WinHTTP 读取状态码失败：%s", formatWinHTTPCallError(callErr))
	}
	out := make([]byte, 0, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return httpResult{}, err
		}
		var available uint32
		if ok, _, callErr := pWinHttpQueryDataAvailable.Call(request, uintptr(unsafe.Pointer(&available))); ok == 0 {
			return httpResult{}, fmt.Errorf("WinHTTP 查询响应失败：%s", formatWinHTTPCallError(callErr))
		}
		if available == 0 {
			break
		}
		if len(out)+int(available) > 8<<20 {
			return httpResult{}, errors.New("接口响应超过 8MB，已终止")
		}
		chunk := make([]byte, available)
		var read uint32
		if ok, _, callErr := pWinHttpReadData.Call(request, uintptr(unsafe.Pointer(&chunk[0])), uintptr(available), uintptr(unsafe.Pointer(&read))); ok == 0 {
			return httpResult{}, fmt.Errorf("WinHTTP 读取响应失败：%s", formatWinHTTPCallError(callErr))
		}
		if read == 0 {
			break
		}
		out = append(out, chunk[:read]...)
	}
	return httpResult{Status: int(status), Body: out, Engine: "Windows 自动代理"}, nil
}

func directHTTPPostJSON(ctx context.Context, raw string, body []byte) (httpResult, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", raw, bytes.NewReader(body))
	if err != nil {
		return httpResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "MultiChainTokenRadar/2.5")
	resp, err := directHTTPClient.Do(req)
	if err != nil {
		return httpResult{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{Status: resp.StatusCode, Body: b, Engine: "Go 直连/环境代理"}, nil
}

func fetchJSONPost(ctx context.Context, raw string, body []byte) (httpResult, error) {
	wr, werr := winHTTPPostJSON(ctx, raw, body)
	if werr == nil {
		return wr, nil
	}
	if err := ctx.Err(); err != nil {
		return httpResult{}, err
	}
	dr, derr := directHTTPPostJSON(ctx, raw, body)
	if derr == nil {
		return dr, nil
	}
	return httpResult{}, fmt.Errorf("Windows 自动代理失败：%v；直连也失败：%v", werr, derr)
}

func rpcCallEndpoint(ctx context.Context, endpoint, method string, params any, out any) (string, error) {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return "", err
	}
	res, err := fetchJSONPost(ctx, endpoint, payload)
	if err != nil {
		return "", err
	}
	if res.Status < 200 || res.Status >= 300 {
		return res.Engine, fmt.Errorf("HTTP %d（%s）", res.Status, res.Engine)
	}
	var env rpcEnvelope
	if err := json.Unmarshal(res.Body, &env); err != nil {
		return res.Engine, fmt.Errorf("RPC JSON 解析失败：%w", err)
	}
	if env.Error != nil {
		return res.Engine, fmt.Errorf("RPC %s 错误 %d：%s", method, env.Error.Code, env.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return res.Engine, fmt.Errorf("RPC %s 结果解析失败：%w", method, err)
		}
	}
	return res.Engine, nil
}

func rpcCall(ctx context.Context, method string, params any, out any) (string, string, error) {
	endpoints := []string{}
	if custom := strings.TrimSpace(os.Getenv("BASE_RPC_URL")); custom != "" {
		endpoints = append(endpoints, custom)
	}
	endpoints = append(endpoints, baseRPCPrimary, baseRPCSecondary)
	var errs []string
	for _, ep := range endpoints {
		engine, err := rpcCallEndpoint(ctx, ep, method, params, out)
		if err == nil {
			return ep, engine, nil
		}
		errs = append(errs, ep+"："+shortErr(err))
		if ctx.Err() != nil {
			break
		}
	}
	return "", "", errors.New(strings.Join(errs, "；"))
}

func parseHexUint(s string) (uint64, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if s == "" {
		return 0, nil
	}
	return strconv.ParseUint(s, 16, 64)
}
func topicAddress(s string) string {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if len(s) < 40 {
		return ""
	}
	return "0x" + s[len(s)-40:]
}
func dataAddress(data string, word int) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(data)), "0x")
	start := word * 64
	if len(s) < start+64 {
		return ""
	}
	return topicAddress(s[start : start+64])
}
func validAddress(a string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	return strings.HasPrefix(a, "0x") && len(a) == 42 && a != "0x0000000000000000000000000000000000000000"
}

func chainCandidatePath() string { return filepath.Join(dataDir(), "chain_candidates_v24.json") }
func loadChainCandidates() chainCandidateFile {
	var f chainCandidateFile
	b, err := os.ReadFile(chainCandidatePath())
	if err != nil {
		// One-time migration keeps the Base block cursor and recent candidates from V2.3.
		b, err = os.ReadFile(filepath.Join(dataDir(), "chain_candidates_v23.json"))
	}
	if err == nil {
		_ = json.Unmarshal(b, &f)
	}
	cutoff := time.Now().Add(-48 * time.Hour)
	out := f.Candidates[:0]
	for _, c := range f.Candidates {
		if c.SeenAt.After(cutoff) && validAddress(c.TokenAddress) {
			out = append(out, c)
		}
	}
	f.Candidates = out
	return f
}
func saveChainCandidates(f chainCandidateFile) {
	f.UpdatedAt = time.Now()
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	tmp := chainCandidatePath() + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		_ = replaceFile(tmp, chainCandidatePath())
	}
}

func scanFactoryLogs(ctx context.Context, from, to uint64, spec factorySpec) ([]chainCandidate, error) {
	filter := map[string]any{"fromBlock": fmt.Sprintf("0x%x", from), "toBlock": fmt.Sprintf("0x%x", to), "address": spec.Address, "topics": []any{spec.Topic}}
	var logs []rpcLog
	_, _, err := rpcCall(ctx, "eth_getLogs", []any{filter}, &logs)
	if err != nil {
		return nil, err
	}
	out := []chainCandidate{}
	for _, lg := range logs {
		if lg.Removed || len(lg.Topics) < 3 {
			continue
		}
		t0, t1 := topicAddress(lg.Topics[1]), topicAddress(lg.Topics[2])
		if !validAddress(t0) || !validAddress(t1) {
			continue
		}
		pool := ""
		if spec.Kind == "v3" {
			pool = dataAddress(lg.Data, 1)
		} else {
			pool = dataAddress(lg.Data, 0)
		}
		if !validAddress(pool) {
			continue
		}
		bn, _ := parseHexUint(lg.BlockNumber)
		add := func(tok string) {
			if !knownBaseAssets[tok] {
				out = append(out, chainCandidate{TokenAddress: tok, PoolAddress: pool, Factory: spec.Name, BlockNumber: bn, SeenAt: time.Now()})
			}
		}
		if knownBaseAssets[t0] && !knownBaseAssets[t1] {
			add(t1)
		} else if knownBaseAssets[t1] && !knownBaseAssets[t0] {
			add(t0)
		} else {
			add(t0)
			add(t1)
		}
	}
	return out, nil
}

func scanCandidateRange(ctx context.Context, from, to uint64) ([]chainCandidate, []string, uint64, bool) {
	found := []chainCandidate{}
	logs := []string{}
	completedThrough := uint64(0)
	if from > 0 {
		completedThrough = from - 1
	}
	for start := from; start <= to; {
		end := start + 599
		if end > to {
			end = to
		}
		chunkOK := true
		for _, spec := range baseFactories {
			cs, e := scanFactoryLogs(ctx, start, end, spec)
			if e != nil {
				chunkOK = false
				logs = append(logs, fmt.Sprintf("%s 区块 %d-%d 查询失败：%s", spec.Name, start, end, shortErr(e)))
				continue
			}
			if len(cs) > 0 {
				logs = append(logs, fmt.Sprintf("%s：发现 %d 个代币候选", spec.Name, len(cs)))
			}
			found = append(found, cs...)
		}
		if !chunkOK {
			logs = append(logs, fmt.Sprintf("区块 %d-%d 未完整读取；扫描游标不会越过失败区间，下次会自动重试", start, end))
			return found, logs, completedThrough, false
		}
		completedThrough = end
		if end == to {
			break
		}
		start = end + 1
	}
	return found, logs, completedThrough, true
}

func discoverBaseCandidates(ctx context.Context) ([]chainCandidate, []string, uint64, int, error) {
	var latestHex string
	endpoint, engine, err := rpcCall(ctx, "eth_blockNumber", []any{}, &latestHex)
	if err != nil {
		return nil, nil, 0, 0, fmt.Errorf("Base RPC 无法读取最新区块：%w", err)
	}
	latest, err := parseHexUint(latestHex)
	if err != nil || latest == 0 {
		return nil, nil, 0, 0, errors.New("Base RPC 返回了无效区块号")
	}
	f := loadChainCandidates()
	originalLast := f.LastBlock
	firstScan := f.LastBlock == 0
	from := f.LastBlock + 1
	if firstScan || (from <= latest && latest-from > 2400) {
		if latest > 1800 {
			from = latest - 1800
		} else {
			from = 0
		}
	}
	logs := []string{fmt.Sprintf("Base RPC：区块 %d，%s，%s", latest, engine, endpoint)}
	if from <= latest {
		logs = append(logs, fmt.Sprintf("链上增量范围：%d → %d", from, latest))
	} else {
		logs = append(logs, "当前没有新增区块；继续复评本地最近候选")
	}

	found := []chainCandidate{}
	primaryComplete := true
	completedThrough := originalLast
	if from <= latest {
		var rangeLogs []string
		var rangeFound []chainCandidate
		rangeFound, rangeLogs, completedThrough, primaryComplete = scanCandidateRange(ctx, from, latest)
		found = append(found, rangeFound...)
		logs = append(logs, rangeLogs...)
	}

	// V2.4 intentionally avoids a multi-hour historical backfill on startup.
	// The recent one-hour window is enough to seed the UI while keeping the free public RPC load bounded.

	// Merge by token address, preferring the newest pool event.
	merged := map[string]chainCandidate{}
	for _, c := range append(f.Candidates, found...) {
		a := strings.ToLower(c.TokenAddress)
		c.TokenAddress = a
		if old, ok := merged[a]; !ok || c.BlockNumber > old.BlockNumber {
			merged[a] = c
		}
	}
	all := make([]chainCandidate, 0, len(merged))
	cutoff := time.Now().Add(-48 * time.Hour)
	for _, c := range merged {
		if c.SeenAt.After(cutoff) {
			all = append(all, c)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].BlockNumber == all[j].BlockNumber {
			return all[i].SeenAt.After(all[j].SeenAt)
		}
		return all[i].BlockNumber > all[j].BlockNumber
	})
	if len(all) > 100 {
		all = all[:100]
	}
	if from > latest {
		// No new block; preserve the cursor.
		f.LastBlock = originalLast
	} else if primaryComplete {
		f.LastBlock = latest
	} else if completedThrough >= from {
		f.LastBlock = completedThrough
	} else {
		f.LastBlock = originalLast
	}
	f.Candidates = all
	saveChainCandidates(f)
	logs = append(logs, fmt.Sprintf("本轮新发现 %d 条；本地保留最近候选 %d 条；游标区块 %d", len(found), len(all), f.LastBlock))
	if len(all) == 0 {
		logs = append(logs, "Base 链连接正常：当前监控范围没有新的受支持交易池")
	}
	return all, logs, latest, len(found), nil
}

func decodeABIString(hexData string) string {
	s := strings.TrimPrefix(hexData, "0x")
	if len(s) < 64 {
		return ""
	}
	// bytes32-returning legacy tokens.
	if len(s) == 64 {
		b := make([]byte, 0, 32)
		for i := 0; i+2 <= len(s); i += 2 {
			v, e := strconv.ParseUint(s[i:i+2], 16, 8)
			if e != nil {
				return ""
			}
			if v == 0 {
				break
			}
			b = append(b, byte(v))
		}
		return strings.TrimSpace(string(b))
	}
	if len(s) < 128 {
		return ""
	}
	n, err := strconv.ParseUint(s[64:128], 16, 64)
	if err != nil || n == 0 || n > 256 {
		return ""
	}
	start := 128
	end := start + int(n)*2
	if end > len(s) {
		return ""
	}
	b := make([]byte, 0, n)
	for i := start; i < end; i += 2 {
		v, e := strconv.ParseUint(s[i:i+2], 16, 8)
		if e != nil {
			return ""
		}
		b = append(b, byte(v))
	}
	return strings.TrimSpace(string(b))
}
func tokenCallString(ctx context.Context, address, selector string) string {
	var result string
	call := map[string]any{"to": address, "data": selector}
	if _, _, err := rpcCall(ctx, "eth_call", []any{call, "latest"}, &result); err != nil {
		return ""
	}
	return decodeABIString(result)
}
func tokenMetadata(ctx context.Context, address string) (string, string) {
	name := tokenCallString(ctx, address, "0x06fdde03")
	symbol := tokenCallString(ctx, address, "0x95d89b41")
	return name, symbol
}

type candidateItem struct {
	ChainID      string `json:"chainId"`
	TokenAddress string `json:"tokenAddress"`
}
type dexSearch struct {
	Pairs []dexPair `json:"pairs"`
}
type dexPair struct {
	ChainID       string                                 `json:"chainId"`
	DEXID         string                                 `json:"dexId"`
	URL           string                                 `json:"url"`
	PairAddress   string                                 `json:"pairAddress"`
	BaseToken     struct{ Address, Name, Symbol string } `json:"baseToken"`
	QuoteToken    struct{ Address, Name, Symbol string } `json:"quoteToken"`
	PriceUSD      string                                 `json:"priceUsd"`
	PairCreatedAt int64                                  `json:"pairCreatedAt"`
	Liquidity     struct {
		USD float64 `json:"usd"`
	} `json:"liquidity"`
	Volume struct {
		H24 float64 `json:"h24"`
	} `json:"volume"`
	PriceChange struct {
		H24 float64 `json:"h24"`
	} `json:"priceChange"`
	Txns struct {
		H24 struct{ Buys, Sells int } `json:"h24"`
	} `json:"txns"`
	Info struct {
		Websites []struct {
			URL string `json:"url"`
		} `json:"websites"`
		Socials []struct{ Platform, Handle string } `json:"socials"`
	} `json:"info"`
}

type scanStats struct {
	LatestBlock   uint64
	ChainSummary  string
	ActiveChains  int
	CandidatePool int
	BatchAnalyzed int
	NewFound      int
	Duration      time.Duration
}

type rotationFile struct {
	Cursor int `json:"cursor"`
}

func rotationPath() string { return filepath.Join(dataDir(), "rotation_v24.json") }
func loadRotation() rotationFile {
	var r rotationFile
	if b, err := os.ReadFile(rotationPath()); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	if r.Cursor < 0 {
		r.Cursor = 0
	}
	return r
}
func saveRotation(r rotationFile) {
	b, _ := json.Marshal(r)
	_ = os.WriteFile(rotationPath(), b, 0644)
}

func chooseCandidateBatch(all []chainCandidate, limit int) []chainCandidate {
	if limit <= 0 || len(all) <= limit {
		return append([]chainCandidate(nil), all...)
	}
	priority := 10
	if priority > limit {
		priority = limit
	}
	if priority > len(all) {
		priority = len(all)
	}
	batch := append([]chainCandidate(nil), all[:priority]...)
	rest := all[priority:]
	if len(rest) == 0 {
		return batch
	}
	r := loadRotation()
	r.Cursor %= len(rest)
	need := limit - len(batch)
	for i := 0; i < need && i < len(rest); i++ {
		batch = append(batch, rest[(r.Cursor+i)%len(rest)])
	}
	r.Cursor = (r.Cursor + need) % len(rest)
	saveRotation(r)
	return batch
}

type marketTokenCache struct {
	SavedAt time.Time `json:"saved_at"`
	Tokens  []Token   `json:"tokens"`
}

func marketTokenCachePath() string { return filepath.Join(dataDir(), "market_tokens_v24.json") }
func loadMarketTokenCache() map[string]Token {
	out := map[string]Token{}
	var cf marketTokenCache
	b, err := os.ReadFile(marketTokenCachePath())
	if err != nil {
		// Migrate the visible V2.3 result cache once, if present.
		if old, e := os.ReadFile(filepath.Join(dataDir(), "cache_v23.json")); e == nil {
			var legacy cacheFile
			if json.Unmarshal(old, &legacy) == nil {
				cf.Tokens = legacy.Results
			}
		}
	} else {
		_ = json.Unmarshal(b, &cf)
	}
	for _, t := range cf.Tokens {
		a := strings.ToLower(strings.TrimSpace(t.Address))
		if validAddress(a) {
			t.Address = a
			out[a] = t
		}
	}
	return out
}
func saveMarketTokenCache(tokens map[string]Token) {
	rows := make([]Token, 0, len(tokens))
	for _, t := range tokens {
		rows = append(rows, t)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].BlockNumber == rows[j].BlockNumber {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].BlockNumber > rows[j].BlockNumber
	})
	if len(rows) > 100 {
		rows = rows[:100]
	}
	b, err := json.MarshalIndent(marketTokenCache{SavedAt: time.Now(), Tokens: rows}, "", "  ")
	if err != nil {
		return
	}
	tmp := marketTokenCachePath() + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		_ = replaceFile(tmp, marketTokenCachePath())
	}
}

func placeholderToken(c chainCandidate, latest uint64) Token {
	age := time.Since(c.SeenAt).Hours()
	if latest >= c.BlockNumber && c.BlockNumber > 0 {
		age = (time.Duration(latest-c.BlockNumber) * 2 * time.Second).Hours()
	}
	if age < 0 {
		age = 0
	}
	a := strings.ToLower(c.TokenAddress)
	sym := "NEW"
	if len(a) >= 8 {
		sym = strings.ToUpper(a[2:8])
	}
	return Token{
		Score: 0, Grade: "等待分析", Name: "Base 链上新代币", Symbol: sym,
		Address: a, AgeHours: age, Security: "待检测", Source: "Base链 · " + c.Factory,
		PoolAddress: c.PoolAddress, BlockNumber: c.BlockNumber,
		DEXURL:   "https://dexscreener.com/base/" + c.PoolAddress,
		Evidence: []string{fmt.Sprintf("已在 Base 链发现新池：%s，区块 %d", c.Factory, c.BlockNumber), "等待进入分批分析队列；不是安全通过或买入信号"},
	}
}

func scanMarket(ctx context.Context, heldAddresses []string) ([]Token, []string, scanStats, error) {
	started := time.Now()
	allCandidates, logs, latestBlock, newFound, err := discoverBaseCandidates(ctx)
	stats := scanStats{LatestBlock: latestBlock, CandidatePool: len(allCandidates), NewFound: newFound}
	if err != nil {
		stats.Duration = time.Since(started)
		return nil, logs, stats, err
	}

	batch := chooseCandidateBatch(allCandidates, 30)
	logs = append(logs, fmt.Sprintf("候选轮询：本轮分析 %d/%d 个；最新 10 个优先，其余候选循环更新", len(batch), len(allCandidates)))
	addresses := make([]string, 0, len(batch)+len(heldAddresses))
	byAddr := map[string]chainCandidate{}
	for _, c := range batch {
		a := strings.ToLower(c.TokenAddress)
		if _, ok := byAddr[a]; !ok {
			addresses = append(addresses, a)
			byAddr[a] = c
		}
	}
	for _, raw := range heldAddresses {
		a := strings.ToLower(strings.TrimSpace(raw))
		if validAddress(a) {
			if _, ok := byAddr[a]; !ok {
				addresses = append(addresses, a)
				byAddr[a] = chainCandidate{TokenAddress: a, Factory: "模拟持仓跟踪", SeenAt: time.Now()}
			}
		}
	}
	stats.BatchAnalyzed = len(addresses)

	universe := map[string]chainCandidate{}
	for _, c := range allCandidates {
		universe[strings.ToLower(c.TokenAddress)] = c
	}
	for _, raw := range heldAddresses {
		a := strings.ToLower(strings.TrimSpace(raw))
		if validAddress(a) {
			if _, ok := universe[a]; !ok {
				universe[a] = chainCandidate{TokenAddress: a, Factory: "模拟持仓跟踪", SeenAt: time.Now()}
			}
		}
	}

	cache := loadMarketTokenCache()
	for a := range cache {
		if _, ok := universe[a]; !ok {
			delete(cache, a)
		}
	}
	for a, c := range universe {
		if old, ok := cache[a]; !ok {
			cache[a] = placeholderToken(c, latestBlock)
		} else {
			old.Source = "Base链 · " + c.Factory
			old.PoolAddress = c.PoolAddress
			old.BlockNumber = c.BlockNumber
			cache[a] = old
		}
	}
	if len(addresses) == 0 {
		saveMarketTokenCache(cache)
		stats.Duration = time.Since(started)
		return topDisplayTokens(cache, 50), logs, stats, nil
	}

	var pairs []dexPair
	for i := 0; i < len(addresses); i += 30 {
		j := i + 30
		if j > len(addresses) {
			j = len(addresses)
		}
		var ps []dexPair
		u := "https://api.dexscreener.com/tokens/v1/base/" + strings.Join(addresses[i:j], ",")
		if e := getJSON(ctx, u, &ps); e != nil {
			logs = append(logs, "DEX 市场补充暂不可用："+shortErr(e))
			break
		}
		pairs = append(pairs, ps...)
	}
	best := map[string]dexPair{}
	for _, p := range pairs {
		a := strings.ToLower(p.BaseToken.Address)
		if _, wanted := byAddr[a]; !wanted {
			continue
		}
		if old, ok := best[a]; !ok || p.Liquidity.USD > old.Liquidity.USD {
			best[a] = p
		}
	}
	logs = append(logs, fmt.Sprintf("DEX Screener：为 %d/%d 个本轮候选补到市场数据", len(best), len(addresses)))
	sec, secErr := fetchSecurity(ctx, addresses)
	if secErr != nil {
		logs = append(logs, "GoPlus 安全接口暂不可用："+shortErr(secErr))
	} else {
		logs = append(logs, fmt.Sprintf("GoPlus：验证 %d 个合约", len(sec)))
	}

	for idx, a := range addresses {
		c := byAddr[a]
		oldCached := cache[a]
		p, ok := best[a]
		metadataChecked := oldCached.MetadataCheckedAt
		if !ok {
			p.ChainID = "base"
			p.DEXID = "chain"
			p.PairAddress = c.PoolAddress
			p.URL = "https://dexscreener.com/base/" + c.PoolAddress
			p.BaseToken.Address = a
			if oldCached.Name != "" && oldCached.Name != "Base 链上新代币" {
				p.BaseToken.Name = oldCached.Name
			}
			if oldCached.Symbol != "" && oldCached.Symbol != "NEW" {
				p.BaseToken.Symbol = oldCached.Symbol
			}
			if idx < 8 && (metadataChecked.IsZero() || time.Since(metadataChecked) >= 6*time.Hour) {
				name, symbol := tokenMetadata(ctx, a)
				if name != "" {
					p.BaseToken.Name = name
				}
				if symbol != "" {
					p.BaseToken.Symbol = symbol
				}
				metadataChecked = time.Now()
			}
			if p.BaseToken.Symbol == "" {
				if len(a) >= 8 {
					p.BaseToken.Symbol = strings.ToUpper(a[2:8])
				} else {
					p.BaseToken.Symbol = "NEW"
				}
			}
			if p.BaseToken.Name == "" {
				p.BaseToken.Name = "Base 链上新代币"
			}
			if latestBlock >= c.BlockNumber && c.BlockNumber > 0 {
				p.PairCreatedAt = time.Now().Add(-time.Duration(latestBlock-c.BlockNumber) * 2 * time.Second).UnixMilli()
			}
		}
		t := scorePair(p, sec[a], sec[a] != nil)
		if !ok {
			if t.Score > 49 {
				t.Score = 49
			}
			t.Grade = "等待市场数据"
			filtered := t.Evidence[:0]
			for _, e := range t.Evidence {
				if strings.Contains(e, "流动性低于") || strings.Contains(e, "24H 成交量很低") {
					continue
				}
				filtered = append(filtered, e)
			}
			t.Evidence = filtered
		}
		t.Source = "Base链 · " + c.Factory
		t.PoolAddress = c.PoolAddress
		t.BlockNumber = c.BlockNumber
		t.UpdatedAt = time.Now()
		t.MetadataCheckedAt = metadataChecked
		t.Evidence = append([]string{fmt.Sprintf("Base 链直接发现：%s 新池，区块 %d", c.Factory, c.BlockNumber)}, t.Evidence...)
		if !ok {
			t.Evidence = append(t.Evidence, "DEX Screener 尚未索引该新池，价格/流动性暂缺；不是零流动性的确认结论")
		}
		cache[a] = t
	}
	saveMarketTokenCache(cache)
	stats.Duration = time.Since(started)
	return topDisplayTokens(cache, 50), logs, stats, nil
}

func topDisplayTokens(cache map[string]Token, limit int) []Token {
	rows := make([]Token, 0, len(cache))
	for _, t := range cache {
		rows = append(rows, t)
	}
	if len(rows) <= limit {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Score == rows[j].Score {
				return rows[i].BlockNumber > rows[j].BlockNumber
			}
			return rows[i].Score > rows[j].Score
		})
		return rows
	}
	// Keep 40 strongest candidates and reserve 10 places for the newest pools,
	// so a fresh unscored pool cannot disappear behind older high-scoring rows.
	byScore := append([]Token(nil), rows...)
	sort.SliceStable(byScore, func(i, j int) bool {
		if byScore[i].Score == byScore[j].Score {
			return byScore[i].BlockNumber > byScore[j].BlockNumber
		}
		return byScore[i].Score > byScore[j].Score
	})
	strongLimit := limit - 10
	if strongLimit < 0 {
		strongLimit = 0
	}
	chosen := make([]Token, 0, limit)
	seen := map[string]bool{}
	for _, t := range byScore {
		if len(chosen) >= strongLimit {
			break
		}
		a := strings.ToLower(t.Address)
		if seen[a] {
			continue
		}
		seen[a] = true
		chosen = append(chosen, t)
	}
	byNewest := append([]Token(nil), rows...)
	sort.SliceStable(byNewest, func(i, j int) bool { return byNewest[i].BlockNumber > byNewest[j].BlockNumber })
	for _, t := range byNewest {
		if len(chosen) >= limit {
			break
		}
		a := strings.ToLower(t.Address)
		if seen[a] {
			continue
		}
		seen[a] = true
		chosen = append(chosen, t)
	}
	return chosen
}

type securityCacheEntry struct {
	Data map[string]any
	At   time.Time
}

var securityCacheMu sync.Mutex
var securityCache = map[string]securityCacheEntry{}

func fetchSecurity(ctx context.Context, addresses []string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	missing := []string{}
	now := time.Now()
	securityCacheMu.Lock()
	for _, raw := range addresses {
		a := strings.ToLower(raw)
		if e, ok := securityCache[a]; ok && now.Sub(e.At) < 30*time.Minute && e.Data != nil {
			out[a] = e.Data
		} else {
			missing = append(missing, a)
		}
	}
	securityCacheMu.Unlock()
	if len(missing) == 0 {
		return out, nil
	}

	var resp struct {
		Code    int                       `json:"code"`
		Message string                    `json:"message"`
		Result  map[string]map[string]any `json:"result"`
	}
	u := "https://api.gopluslabs.io/api/v1/token_security/8453?contract_addresses=" + url.QueryEscape(strings.Join(missing, ","))
	if err := getJSON(ctx, u, &resp); err != nil {
		return out, err
	}
	if resp.Code != 1 || resp.Result == nil {
		return out, fmt.Errorf("GoPlus 返回业务错误：code=%d message=%s", resp.Code, resp.Message)
	}
	securityCacheMu.Lock()
	for k, v := range resp.Result {
		a := strings.ToLower(k)
		out[a] = v
		securityCache[a] = securityCacheEntry{Data: v, At: now}
	}
	securityCacheMu.Unlock()
	return out, nil
}

func val(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}
func isOne(m map[string]any, k string) bool {
	return val(m, k) == "1" || strings.EqualFold(val(m, k), "true")
}
func fnum(s string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return v }
func holderConcentration(sec map[string]any) (maxHolder, topUnlocked float64, count int) {
	raw, ok := sec["holders"].([]any)
	if !ok {
		return 0, 0, 0
	}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tag := strings.ToLower(val(m, "tag"))
		locked := isOne(m, "is_locked")
		if locked || strings.Contains(tag, "burn") || strings.Contains(tag, "dead") || strings.Contains(tag, "black hole") {
			continue
		}
		pct := fnum(val(m, "percent"))
		if pct <= 0 {
			continue
		}
		count++
		topUnlocked += pct
		if pct > maxHolder {
			maxHolder = pct
		}
	}
	return maxHolder, topUnlocked, count
}

func lpLockPercent(sec map[string]any) (locked float64, known bool) {
	raw, ok := sec["lp_holders"].([]any)
	if !ok {
		return 0, false
	}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		pct := fnum(val(m, "percent"))
		if pct <= 0 {
			continue
		}
		known = true
		if isOne(m, "is_locked") {
			locked += pct
		}
	}
	return locked, known
}

func scorePair(p dexPair, sec map[string]any, verified bool) Token {
	priceV := fnum(p.PriceUSD)
	age := 0.0
	if p.PairCreatedAt > 0 {
		age = time.Since(time.UnixMilli(p.PairCreatedAt)).Hours()
		if age < 0 {
			age = 0
		}
	}
	score := 0
	ev := []string{"质量分用于筛选与排雷，不是自动买入信号"}
	severe := false
	securityCap := 100

	// Market quality: liquidity, real activity, pool age and basic project links.
	switch {
	case p.Liquidity.USD >= 1_000_000:
		score += 22
		ev = append(ev, "流动性达到 100 万美元以上")
	case p.Liquidity.USD >= 200_000:
		score += 19
		ev = append(ev, "流动性达到 20 万美元以上")
	case p.Liquidity.USD >= 50_000:
		score += 14
		ev = append(ev, "流动性达到基础观察门槛")
	case p.Liquidity.USD >= 10_000:
		score += 6
		ev = append(ev, "流动性偏低，成交滑点可能较大")
	default:
		ev = append(ev, "流动性低于 1 万美元，撤池与滑点风险很高")
	}

	switch {
	case p.Volume.H24 >= 1_000_000:
		score += 14
	case p.Volume.H24 >= 200_000:
		score += 12
	case p.Volume.H24 >= 20_000:
		score += 8
	case p.Volume.H24 >= 5_000:
		score += 3
	default:
		ev = append(ev, "24H 成交活跃度不足")
	}
	if p.Liquidity.USD > 0 {
		turnover := p.Volume.H24 / p.Liquidity.USD
		if turnover >= 0.15 && turnover <= 6 {
			score += 4
			ev = append(ev, fmt.Sprintf("成交/流动性比 %.2f，处于可观察范围", turnover))
		} else if turnover > 12 {
			score -= 5
			ev = append(ev, fmt.Sprintf("成交/流动性比 %.2f 过高，需警惕刷量或剧烈换手", turnover))
		}
	}

	switch {
	case age >= 24*30:
		score += 10
	case age >= 24*3:
		score += 7
	case age >= 24:
		score += 3
	default:
		ev = append(ev, "交易池建立不足 24 小时")
	}

	trades := p.Txns.H24.Buys + p.Txns.H24.Sells
	switch {
	case trades >= 1000:
		score += 8
	case trades >= 100:
		score += 5
	case trades >= 20:
		score += 2
	default:
		ev = append(ev, "24H 交易笔数偏少")
	}
	if p.Txns.H24.Buys > 0 && p.Txns.H24.Sells > 0 {
		ratio := float64(p.Txns.H24.Buys) / float64(p.Txns.H24.Sells)
		if ratio >= 0.55 && ratio <= 2.2 {
			score += 3
		} else if ratio > 4 || ratio < 0.25 {
			score -= 4
			ev = append(ev, fmt.Sprintf("买卖笔数比 %.2f 极端，需排查机器人或单边出货", ratio))
		}
	}

	absChange := math.Abs(p.PriceChange.H24)
	if absChange <= 35 {
		score += 3
	} else if absChange > 100 {
		score -= 5
		ev = append(ev, "24H 波动超过 100%，不适合直接追价")
	}
	if len(p.Info.Websites) > 0 {
		score += 3
		ev = append(ev, "市场资料包含项目网站")
	}
	if len(p.Info.Socials) > 0 {
		score += 3
		ev = append(ev, "市场资料包含社交入口")
	}

	security := "安全未验证"
	buyTaxPct, sellTaxPct := 0.0, 0.0
	taxKnown := false
	if verified && sec != nil {
		security = "已验证"
		score += 8
		if isOne(sec, "is_open_source") {
			score += 6
			ev = append(ev, "合约代码已开源")
		} else if val(sec, "is_open_source") == "0" {
			score -= 12
			securityCap = minInt(securityCap, 45)
			ev = append(ev, "合约未开源，安全检测能力受限")
		}

		risks := []struct {
			key, label string
			penalty    int
			hard       bool
		}{
			{"is_honeypot", "疑似貔貅盘", 0, true},
			{"cannot_sell_all", "可能无法完全卖出", 0, true},
			{"is_blacklisted", "合约存在黑名单限制", 0, true},
			{"owner_change_balance", "所有者可修改用户余额", 0, true},
			{"selfdestruct", "合约存在自毁能力", 0, true},
			{"hidden_owner", "存在隐藏所有者", -14, false},
			{"transfer_pausable", "可暂停转账", -10, false},
			{"is_proxy", "代理合约，可升级实现逻辑", -4, false},
			{"is_mintable", "仍可增发代币", -7, false},
			{"slippage_modifiable", "交易税或滑点可被修改", -9, false},
			{"personal_slippage_modifiable", "可针对单个地址修改税费", -12, false},
			{"trading_cooldown", "存在交易冷却限制", -4, false},
			{"anti_whale_modifiable", "反巨鲸规则可修改", -5, false},
			{"external_call", "转账逻辑包含外部调用", -5, false},
		}
		for _, r := range risks {
			if isOne(sec, r.key) {
				ev = append(ev, r.label)
				score += r.penalty
				if r.hard {
					severe = true
				}
			}
		}

		buyRaw, sellRaw := val(sec, "buy_tax"), val(sec, "sell_tax")
		if buyRaw != "" && sellRaw != "" {
			buyTax := fnum(buyRaw) * 100
			sellTax := fnum(sellRaw) * 100
			buyTaxPct, sellTaxPct, taxKnown = buyTax, sellTax, true
			switch {
			case buyTax <= 5 && sellTax <= 5:
				score += 6
				ev = append(ev, fmt.Sprintf("买卖税较低：%.1f%% / %.1f%%", buyTax, sellTax))
			case buyTax > 20 || sellTax > 20:
				severe = true
				ev = append(ev, fmt.Sprintf("买卖税极高：%.1f%% / %.1f%%", buyTax, sellTax))
			default:
				score -= 8
				ev = append(ev, fmt.Sprintf("买卖税偏高：%.1f%% / %.1f%%", buyTax, sellTax))
			}
		} else {
			ev = append(ev, "安全接口未返回完整买卖税数据，不加安全分")
		}

		maxHolder, top10, holderN := holderConcentration(sec)
		if holderN > 0 {
			ev = append(ev, fmt.Sprintf("未锁定头部地址：最大 %.1f%%，前列合计 %.1f%%", maxHolder*100, top10*100))
			switch {
			case maxHolder <= 0.05 && top10 <= 0.35:
				score += 8
			case maxHolder <= 0.10 && top10 <= 0.50:
				score += 3
			case maxHolder > 0.20 || top10 > 0.70:
				score -= 12
				securityCap = minInt(securityCap, 55)
				ev = append(ev, "持币集中度过高，存在大户砸盘风险")
			default:
				score -= 4
			}
		} else {
			ev = append(ev, "未取得可用的头部持仓分布")
		}

		ownerPct := math.Max(fnum(val(sec, "owner_percent")), fnum(val(sec, "creator_percent")))
		if ownerPct > 0 {
			switch {
			case ownerPct <= 0.03:
				score += 4
			case ownerPct > 0.20:
				score -= 10
				securityCap = minInt(securityCap, 55)
				ev = append(ev, fmt.Sprintf("所有者/创建者持仓较高：%.1f%%", ownerPct*100))
			default:
				ev = append(ev, fmt.Sprintf("所有者/创建者持仓：%.1f%%", ownerPct*100))
			}
		}

		if locked, known := lpLockPercent(sec); known {
			if locked >= 0.80 {
				score += 4
				ev = append(ev, fmt.Sprintf("已识别 LP 锁定比例约 %.1f%%", locked*100))
			} else {
				score -= 5
				ev = append(ev, fmt.Sprintf("已识别 LP 锁定比例仅约 %.1f%%", locked*100))
			}
		} else {
			ev = append(ev, "未取得可靠的 LP 锁定比例")
		}
		if c := val(sec, "holder_count"); c != "" {
			ev = append(ev, "GoPlus 持币地址数："+c)
		}
	} else {
		securityCap = minInt(securityCap, 64)
		ev = append(ev, "未取得安全接口结果，质量分强制不高于 64")
	}

	if severe {
		score = 0
		security = "严重风险"
	}
	if score > securityCap {
		score = securityCap
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	grade := "高风险"
	if score >= 80 {
		grade = "重点观察"
	} else if score >= 65 {
		grade = "进入观察"
	} else if score >= 50 {
		grade = "谨慎复核"
	} else if score == 0 && severe {
		grade = "硬性淘汰"
	}
	return Token{
		Score: score, Grade: grade, Name: p.BaseToken.Name, Symbol: p.BaseToken.Symbol,
		Address: strings.ToLower(p.BaseToken.Address), Price: priceV, Liquidity: p.Liquidity.USD,
		Volume24: p.Volume.H24, AgeHours: age, Change24: p.PriceChange.H24,
		Buys24: p.Txns.H24.Buys, Sells24: p.Txns.H24.Sells,
		BuyTaxPct: buyTaxPct, SellTaxPct: sellTaxPct, TaxKnown: taxKnown,
		Security: security, Evidence: ev, DEXURL: p.URL,
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func diagnose() string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var b strings.Builder
	b.WriteString("连接诊断结果（V2.5 多链版）\n\n")
	b.WriteString("网络引擎：Windows WinHTTP 自动代理优先，Go 直连/环境代理兜底\n")
	b.WriteString("系统代理：" + windowsProxySummary() + "\n\n")
	okCount := 0
	st := time.Now()
	var block string
	ep, engine, err := rpcCall(ctx, "eth_blockNumber", []any{}, &block)
	if err != nil {
		b.WriteString("✕ Base RPC：无法读取区块\n   " + fullErr(err) + "\n\n")
	} else {
		okCount++
		n, _ := parseHexUint(block)
		b.WriteString(fmt.Sprintf("✓ Base RPC：正常，最新区块 %d，%s，耗时 %d ms\n   %s\n\n", n, engine, time.Since(st).Milliseconds(), ep))
	}
	tests := []struct{ name, url string }{{"DEX Screener（市场补充）", "https://api.dexscreener.com/token-profiles/latest/v1"}, {"GoPlus（合约安全）", "https://api.gopluslabs.io/api/v1/token_security/8453?contract_addresses=0x4200000000000000000000000000000000000006"}}
	for _, t := range tests {
		st = time.Now()
		res, e := fetchURL(ctx, t.url)
		if e != nil {
			b.WriteString("✕ " + t.name + "：连接失败\n   " + fullErr(e) + "\n\n")
			continue
		}
		if res.Status >= 200 && res.Status < 300 {
			okCount++
			b.WriteString(fmt.Sprintf("✓ %s：正常，HTTP %d，%s，耗时 %d ms\n\n", t.name, res.Status, res.Engine, time.Since(st).Milliseconds()))
		} else {
			body := strings.TrimSpace(string(res.Body))
			if len([]rune(body)) > 180 {
				body = string([]rune(body)[:180]) + "…"
			}
			b.WriteString(fmt.Sprintf("△ %s：可达但返回 HTTP %d（%s）\n   %s\n\n", t.name, res.Status, res.Engine, body))
		}
	}
	if okCount == 3 {
		b.WriteString("结论：Base 链发现、市场补充和安全排雷全部可用。可以关闭诊断后点击“立即扫描”。")
	} else if okCount >= 1 {
		b.WriteString("结论：部分可用。只要 Base RPC 正常，程序仍能发现新池；DEX失败会缺少市场指标，GoPlus失败会标记“安全未验证”。")
	} else {
		b.WriteString("结论：当前三个来源均不可用。请确认代理软件已开启系统代理，或在环境变量 BASE_RPC_URL 中配置可用的 Base RPC。")
	}
	return b.String()
}

func tokensToQuotes(tokens []Token, now time.Time) []SimQuote {
	out := make([]SimQuote, 0, len(tokens))
	for _, t := range tokens {
		quoteTime := t.UpdatedAt
		if quoteTime.IsZero() {
			quoteTime = now
		}
		out = append(out, SimQuote{Chain: t.Chain, Address: t.Address, Symbol: t.Symbol, Name: t.Name, Price: t.Price, Liquidity: t.Liquidity, BuyTaxPct: t.BuyTaxPct, SellTaxPct: t.SellTaxPct, TaxKnown: t.TaxKnown, Score: t.Score, Security: t.Security, Buys: t.Buys24, Sells: t.Sells24, Volume24: t.Volume24, AgeHours: t.AgeHours, Source: t.Source, Time: quoteTime})
	}
	return out
}

func canManualSimBuy() bool {
	if app.sim == nil || app.selected < 0 || app.selected >= len(app.results) {
		return false
	}
	t := app.results[app.selected]
	return t.Price > 0 && t.Liquidity > 0 && t.Security != "严重风险" && !strings.Contains(t.Source, "演示") && !app.sim.hasPositionOn(t.Chain, t.Address) && len(app.sim.Positions) < app.sim.Config.MaxPositions && app.sim.Cash >= app.sim.Config.PositionSize
}

func selectedPositionValid() bool {
	return app.sim != nil && app.selectedPos >= 0 && app.selectedPos < len(app.sim.Positions)
}

func manualSimBuy() {
	if app.selected < 0 || app.selected >= len(app.results) {
		return
	}
	t := app.results[app.selected]
	q := tokensToQuotes([]Token{t}, time.Now())[0]
	p, err := app.sim.Buy(q, app.sim.Config.PositionSize, "用户手动模拟买入", time.Now())
	if err != nil {
		app.toast = "模拟买入失败：" + shortErr(err)
		app.toastUntil = time.Now().Add(4 * time.Second)
		invalidate(false)
		return
	}
	app.toast = fmt.Sprintf("已模拟买入 %s，仓位 %.2f USDC", p.Symbol, p.EntryCost)
	app.toastUntil = time.Now().Add(4 * time.Second)
	addLog(fmt.Sprintf("模拟买入 %s：%.2f USDC，执行价 %s", p.Symbol, p.EntryCost, price(p.EntryPrice)))
	saveSimState()
	invalidate(false)
}

func quoteForPositionNow(p SimPosition) SimQuote {
	return app.sim.quoteForPosition(p, tokensToQuotes(app.results, time.Now()))
}

func manualSimSellSelected() {
	if !selectedPositionValid() {
		return
	}
	p := app.sim.Positions[app.selectedPos]
	q := quoteForPositionNow(p)
	tr, err := app.sim.Sell(p.ID, 1, q, "用户手动平仓", time.Now())
	if err != nil {
		app.toast = "模拟卖出失败：" + shortErr(err)
		app.toastUntil = time.Now().Add(4 * time.Second)
		invalidate(false)
		return
	}
	app.selectedPos = -1
	app.toast = fmt.Sprintf("%s 已平仓，净盈亏 %+.2f USDC", tr.Symbol, tr.PnL)
	app.toastUntil = time.Now().Add(4 * time.Second)
	addLog(fmt.Sprintf("模拟平仓 %s：净盈亏 %+.2f USDC", tr.Symbol, tr.PnL))
	saveSimState()
	invalidate(false)
}

func closeAllSimPositions(reason string) {
	if app.sim == nil || len(app.sim.Positions) == 0 {
		return
	}
	ids := make([]int64, 0, len(app.sim.Positions))
	for _, p := range app.sim.Positions {
		ids = append(ids, p.ID)
	}
	closed := 0
	total := 0.0
	for _, id := range ids {
		var p *SimPosition
		for i := range app.sim.Positions {
			if app.sim.Positions[i].ID == id {
				p = &app.sim.Positions[i]
				break
			}
		}
		if p == nil {
			continue
		}
		q := quoteForPositionNow(*p)
		tr, err := app.sim.Sell(id, 1, q, reason, time.Now())
		if err == nil {
			closed++
			total += tr.PnL
		}
	}
	app.selectedPos = -1
	app.toast = fmt.Sprintf("已平仓 %d 个持仓，净盈亏 %+.2f USDC", closed, total)
	app.toastUntil = time.Now().Add(5 * time.Second)
	addLog(app.toast)
	saveSimState()
	invalidate(false)
}

func simStatePath() string { return filepath.Join(dataDir(), "simulation_v25.json") }
func saveSimState() {
	if app.sim == nil {
		return
	}
	b, err := json.MarshalIndent(app.sim, "", "  ")
	if err != nil {
		return
	}
	tmp := simStatePath() + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		_ = replaceFile(tmp, simStatePath())
	}
}
func loadSimState() {
	b, err := os.ReadFile(simStatePath())
	if err != nil {
		b, err = os.ReadFile(filepath.Join(dataDir(), "simulation_v24.json"))
		if err != nil {
			b, err = os.ReadFile(filepath.Join(dataDir(), "simulation_v23.json"))
		}
	}
	if err != nil {
		app.sim = NewSimState()
		return
	}
	var st SimState
	if json.Unmarshal(b, &st) != nil {
		app.sim = NewSimState()
		addLog("模拟账户文件损坏，已创建新的 100 USDC 模拟账户")
		return
	}
	st.Normalize()
	app.sim = &st
	addLog(fmt.Sprintf("已加载模拟账户：余额 %.2f USDC，持仓 %d 个，历史 %d 条", st.Cash, len(st.Positions), len(st.Trades)))
}

type preferencesFile struct {
	Realtime bool `json:"realtime"`
}

func preferencesPath() string { return filepath.Join(dataDir(), "preferences_v25.json") }
func savePreferences() {
	b, _ := json.Marshal(preferencesFile{Realtime: app.autoRefresh})
	_ = os.WriteFile(preferencesPath(), b, 0644)
}
func loadPreferences() {
	b, err := os.ReadFile(preferencesPath())
	if err != nil {
		b, err = os.ReadFile(filepath.Join(dataDir(), "preferences_v24.json"))
		if err != nil {
			b, err = os.ReadFile(filepath.Join(dataDir(), "preferences_v23.json"))
		}
	}
	if err != nil {
		return
	}
	var p preferencesFile
	if json.Unmarshal(b, &p) == nil {
		app.autoRefresh = p.Realtime
	}
}

func exportSimCSV() {
	if app.sim == nil || len(app.sim.Trades) == 0 {
		return
	}
	home, _ := os.UserHomeDir()
	desktop := filepath.Join(home, "Desktop")
	if _, err := os.Stat(desktop); err != nil {
		desktop = home
	}
	path := filepath.Join(desktop, "MultiChainRadar_SimTrades_"+time.Now().Format("20060102_150405")+".csv")
	f, err := os.Create(path)
	if err != nil {
		app.toast = "导出失败：" + shortErr(err)
		app.toastUntil = time.Now().Add(4 * time.Second)
		invalidate(false)
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(f)
	_ = w.Write([]string{"平仓时间", "链", "代币", "合约", "买入价", "卖出价", "成本", "净回款", "费用估算", "净盈亏", "收益率", "退出原因"})
	for _, t := range app.sim.Trades {
		_ = w.Write([]string{t.ClosedAt.Format("2006-01-02 15:04:05"), chainLabel(t.Chain), t.Symbol, t.Address, strconv.FormatFloat(t.EntryPrice, 'f', 10, 64), strconv.FormatFloat(t.ExitPrice, 'f', 10, 64), fmt.Sprintf("%.4f", t.CostAllocated), fmt.Sprintf("%.4f", t.NetProceeds), fmt.Sprintf("%.4f", t.Fees), fmt.Sprintf("%.4f", t.PnL), fmt.Sprintf("%.2f%%", t.PnLPct), t.Reason})
	}
	w.Flush()
	app.toast = "模拟交易记录已导出到桌面"
	app.toastUntil = time.Now().Add(4 * time.Second)
	addLog("模拟交易记录已导出：" + path)
	invalidate(false)
}

// ----------------------------- Persistence and helpers -----------------------------

func dataDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	p := filepath.Join(d, "BaseTokenRadar")
	_ = os.MkdirAll(p, 0755)
	return p
}
func cachePath() string { return filepath.Join(dataDir(), "cache_v25.json") }

type cacheFile struct {
	SavedAt time.Time `json:"saved_at"`
	Results []Token   `json:"results"`
}

func saveCache() {
	cf := cacheFile{SavedAt: app.lastScan, Results: app.results}
	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		addLog("缓存序列化失败：" + fullErr(err))
		return
	}
	tmp := cachePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		addLog("缓存写入失败：" + fullErr(err))
		return
	}
	if err := replaceFile(tmp, cachePath()); err != nil {
		_ = os.Remove(tmp)
		addLog("缓存替换失败：" + fullErr(err))
	}
}

func loadCache() {
	b, err := os.ReadFile(cachePath())
	if err != nil {
		b, err = os.ReadFile(filepath.Join(dataDir(), "cache_v24.json"))
		if err != nil {
			b, err = os.ReadFile(filepath.Join(dataDir(), "cache_v23.json"))
		}
	}
	if err != nil {
		return
	}
	var cf cacheFile
	if json.Unmarshal(b, &cf) == nil && len(cf.Results) > 0 {
		app.results = cf.Results
		app.selected = 0
		app.lastScan = cf.SavedAt
		app.lastSuccess = cf.SavedAt
		app.status = "已加载上次实时结果"
		app.statusKind = 2
		age := "未知时间"
		if !cf.SavedAt.IsZero() {
			age = cf.SavedAt.Format("01-02 15:04")
		}
		addLog(fmt.Sprintf("已加载本地缓存：%d 个候选；缓存时间 %s", len(cf.Results), age))
		return
	}
	// Defensive compatibility with old array-only caches.
	var old []Token
	if json.Unmarshal(b, &old) == nil && len(old) > 0 {
		app.results = old
		app.selected = 0
		app.status = "已加载旧版缓存"
		app.statusKind = 2
		addLog(fmt.Sprintf("已加载旧版缓存：%d 个候选；时间未知", len(old)))
	}
}
func rotateRuntimeLog(path string) {
	if st, err := os.Stat(path); err == nil && st.Size() > 2*1024*1024 {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
}
func addLog(s string) {
	line := time.Now().Format("2006-01-02 15:04:05  ") + s
	app.logs = append(app.logs, time.Now().Format("15:04:05  ")+s)
	if len(app.logs) > 100 {
		app.logs = app.logs[len(app.logs)-100:]
	}
	logPath := filepath.Join(dataDir(), "runtime_v211.log")
	rotateRuntimeLog(logPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		_, _ = f.WriteString(line + "\r\n")
		_ = f.Close()
	}
}
func verifiedCount() string {
	n := 0
	for _, t := range app.results {
		if t.Security == "已验证" {
			n++
		}
	}
	return strconv.Itoa(n)
}
func watchCount() string {
	if app.monitor == nil {
		return "0"
	}
	return strconv.Itoa(app.monitor.Count())
}
func lastUpdateText() string {
	if app.lastScan.IsZero() {
		return "尚未扫描"
	}
	return app.lastScan.Format("15:04:05")
}
func dataFreshness(t time.Time) string {
	if t.IsZero() {
		return "等待分析"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%d秒前", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d分钟前", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1f小时前", d.Hours())
}
func statusBarText() string {
	if app.scanning {
		return "扫描中：Base 链与补充接口最长等待 55 秒；可随时点击“停止扫描”。"
	}
	if app.statusKind == 3 {
		return "Base链或补充接口暂不可用，已保留上次结果；请使用连接诊断。"
	}
	return "就绪 · 5秒实时监控；模拟盘不连接钱包、不发送真实交易。"
}
func onOff(b bool) string {
	if b {
		return "开启"
	}
	return "关闭"
}
func fullErr(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ReplaceAll(err.Error(), "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) > 500 {
		s = string([]rune(s)[:500]) + "…"
	}
	return s
}

func shortErr(err error) string {
	s := err.Error()
	if len([]rune(s)) > 140 {
		s = string([]rune(s)[:140]) + "…"
	}
	return s
}
func money(v float64) string {
	if v >= 1e9 {
		return fmt.Sprintf("$%.2fB", v/1e9)
	}
	if v >= 1e6 {
		return fmt.Sprintf("$%.2fM", v/1e6)
	}
	if v >= 1e3 {
		return fmt.Sprintf("$%.1fK", v/1e3)
	}
	return fmt.Sprintf("$%.0f", v)
}
func price(v float64) string {
	if v == 0 {
		return "—"
	}
	if v < 0.0001 {
		return fmt.Sprintf("$%.8f", v)
	}
	if v < 1 {
		return fmt.Sprintf("$%.6f", v)
	}
	return fmt.Sprintf("$%.4f", v)
}
func ageText(h float64) string {
	if h >= 24*365 {
		return fmt.Sprintf("%.1f年", h/(24*365))
	}
	if h >= 24 {
		return fmt.Sprintf("%.1f天", h/24)
	}
	return fmt.Sprintf("%.0f小时", h)
}
func shortAddr(a string) string {
	if len(a) > 18 {
		return a[:10] + "…" + a[len(a)-6:]
	}
	return a
}

func loadDemo() {
	app.results = []Token{
		{Chain: "base", ChainID: "8453", Score: 88, Grade: "重点观察", Name: "Aerodrome Finance", Symbol: "AERO", Address: "0x940181a94a35a4569e4529a3cdfb74e38fd98631", Price: 0.72, Liquidity: 8_200_000, Volume24: 12_400_000, AgeHours: 24 * 720, Change24: 3.8, Buys24: 4132, Sells24: 3650, BuyTaxPct: 0, SellTaxPct: 0, TaxKnown: true, Security: "已验证", Evidence: []string{"演示：Base 合约安全字段未发现硬性风险", "演示：流动性和成交量较充足", "演示：项目资料与社交入口完整", "仍需人工核对持币集中度与最新权限"}, DEXURL: "https://dexscreener.com/base/0x...", Source: "演示数据"},
		{Chain: "bsc", ChainID: "56", Score: 78, Grade: "进入观察", Name: "PancakeSwap", Symbol: "CAKE", Address: "0x0e09fabb73bd3ade0a17ecc321fd13a19e81ce82", Price: 2.85, Liquidity: 6_500_000, Volume24: 9_100_000, AgeHours: 24 * 1000, Change24: 2.4, Buys24: 3380, Sells24: 3010, BuyTaxPct: 0, SellTaxPct: 0, TaxKnown: true, Security: "已验证", Evidence: []string{"演示：BSC 模块返回候选", "演示：已完成安全验证", "标准档仍需等待短线价格结构"}, DEXURL: "https://dexscreener.com/bsc/0x...", Source: "演示数据"},
		{Chain: "arbitrum", ChainID: "42161", Score: 72, Grade: "进入观察", Name: "Arbitrum", Symbol: "ARB", Address: "0x912ce59144191c1204e64559fe8253a0e49e6548", Price: 0.42, Liquidity: 4_200_000, Volume24: 7_300_000, AgeHours: 24 * 850, Change24: -1.8, Buys24: 2500, Sells24: 2630, BuyTaxPct: 0, SellTaxPct: 0, TaxKnown: true, Security: "已验证", Evidence: []string{"演示：Arbitrum 模块返回候选", "演示：安全检查通过基础项", "买入笔数暂低于卖出，自动策略不会开仓"}, DEXURL: "https://dexscreener.com/arbitrum/0x...", Source: "演示数据"},
		{Chain: "bsc", ChainID: "56", Score: 58, Grade: "测试候选", Name: "New BSC Project", Symbol: "NBP", Address: "0x1111111111111111111111111111111111111111", Price: 0.00042, Liquidity: 28_000, Volume24: 34_000, AgeHours: 36, Change24: 5.0, Buys24: 321, Sells24: 178, Security: "安全未验证", Evidence: []string{"演示：测试档可用于验证模拟流程", "演示：安全未验证，不能视为低风险", "严重风险仍会被所有档位淘汰"}, DEXURL: "https://dexscreener.com/bsc/0x...", Source: "演示数据"},
		{Chain: "arbitrum", ChainID: "42161", Score: 0, Grade: "硬性淘汰", Name: "Honeypot Example", Symbol: "BAD", Address: "0x3333333333333333333333333333333333333333", Price: 0.004, Liquidity: 46_000, Volume24: 120_000, AgeHours: 30, Change24: 62, Buys24: 800, Sells24: 12, BuyTaxPct: 0, SellTaxPct: 100, TaxKnown: true, Security: "严重风险", Evidence: []string{"演示：疑似貔貅盘", "演示：可能无法正常卖出", "严重风险触发硬性淘汰"}, DEXURL: "https://dexscreener.com/arbitrum/0x...", Source: "演示数据"},
	}
	now := time.Now()
	for i := range app.results {
		app.results[i].UpdatedAt = now
	}
	app.selected = 0
	resetListPosition()
	app.filterMode = 0
	app.sortMode = 0
	app.searchText = ""
	app.candidatePool = len(app.results)
	app.lastBatchAnalyzed = len(app.results)
	app.lastScanDuration = 1200 * time.Millisecond
	app.lastSuccess = now
	app.status = "已加载演示数据"
	app.statusKind = 1
	addLog("已加载 5 条演示数据；演示数据不会写入正式模拟账户")
	invalidate(false)
}

func exportCSV() {
	if len(app.results) == 0 {
		return
	}
	home, _ := os.UserHomeDir()
	desktop := filepath.Join(home, "Desktop")
	if _, err := os.Stat(desktop); err != nil {
		desktop = home
	}
	path := filepath.Join(desktop, "MultiChainRadar_"+time.Now().Format("20060102_150405")+".csv")
	f, err := os.Create(path)
	if err != nil {
		app.toast = "导出失败：" + shortErr(err)
		app.toastUntil = time.Now().Add(4 * time.Second)
		invalidate(false)
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(f)
	_ = w.Write([]string{"链", "质量分", "等级", "代币", "名称", "合约", "走势图链接", "区块浏览器链接", "价格", "流动性", "24H成交", "池龄小时", "24H涨跌", "买入税%", "卖出税%", "税费已确认", "安全", "来源"})
	for _, t := range app.results {
		_ = w.Write([]string{chainLabel(t.Chain), strconv.Itoa(t.Score), t.Grade, t.Symbol, t.Name, t.Address, selectedDEXURL(t), explorerTokenURL(t.Chain, t.Address), strconv.FormatFloat(t.Price, 'f', 8, 64), strconv.FormatFloat(t.Liquidity, 'f', 2, 64), strconv.FormatFloat(t.Volume24, 'f', 2, 64), strconv.FormatFloat(t.AgeHours, 'f', 1, 64), strconv.FormatFloat(t.Change24, 'f', 2, 64), strconv.FormatFloat(t.BuyTaxPct, 'f', 2, 64), strconv.FormatFloat(t.SellTaxPct, 'f', 2, 64), strconv.FormatBool(t.TaxKnown), t.Security, t.Source})
	}
	w.Flush()
	app.toast = "CSV 已导出到桌面"
	app.toastUntil = time.Now().Add(3 * time.Second)
	addLog("CSV 已导出：" + path)
	invalidate(false)
}
func openSelectedDEX() {
	if app.selected < 0 || app.selected >= len(app.results) {
		return
	}
	u := selectedDEXURL(app.results[app.selected])
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16Ptr("open"))), uintptr(unsafe.Pointer(utf16Ptr(u))), 0, 0, SW_SHOWNORMAL)
}

func selectedDEXURL(t Token) string {
	u := strings.TrimSpace(t.DEXURL)
	if !strings.HasPrefix(u, "http") {
		u = dexURLFor(t.Chain, t.Address)
	}
	return u
}

func explorerTokenURL(chain, address string) string {
	base := "https://basescan.org/token/"
	switch normalizeChain(chain) {
	case "bsc":
		base = "https://bscscan.com/token/"
	case "arbitrum":
		base = "https://arbiscan.io/token/"
	}
	return base + strings.TrimSpace(address)
}

func openSelectedExplorer() {
	if app.selected < 0 || app.selected >= len(app.results) {
		return
	}
	u := explorerTokenURL(app.results[app.selected].Chain, app.results[app.selected].Address)
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16Ptr("open"))), uintptr(unsafe.Pointer(utf16Ptr(u))), 0, 0, SW_SHOWNORMAL)
}

func main() {
	// Win32 windows and their message queues are thread-affine. Keep the entire
	// UI loop on one OS thread; otherwise the Go scheduler may migrate the
	// goroutine between GetMessage calls and make the window appear frozen.
	runtime.LockOSThread()
	// Per-monitor v2 DPI awareness. -4 is DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2.
	pSetProcessDpiAwarenessContext.Call(^uintptr(3))
	hInst, _, _ := pGetModuleHandleW.Call(0)
	className := utf16Ptr("MultiChainTokenRadarV211Window")
	title := utf16Ptr("Multi-Chain Token Radar V2.11 · 探索样本与拒绝统计")
	cur, _, _ := pLoadCursorW.Call(0, IDC_ARROW)
	wc := WNDCLASSEX{CbSize: uint32(unsafe.Sizeof(WNDCLASSEX{})), Style: 0x0008, LpfnWndProc: syscall.NewCallback(wndProc), HInstance: HINSTANCE(hInst), HCursor: HCURSOR(cur), HbrBackground: 0, LpszClassName: className}
	if r, _, e := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		panic(e)
	}
	hwnd, _, e := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), WS_OVERLAPPEDWINDOW|WS_VISIBLE, CW_USEDEFAULT, CW_USEDEFAULT, 1400, 900, 0, 0, hInst, 0)
	if hwnd == 0 {
		panic(e)
	}
	pShowWindow.Call(hwnd, SW_MAXIMIZE)
	pUpdateWindow.Call(hwnd)
	var m MSG
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

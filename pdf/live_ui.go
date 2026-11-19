package pdf

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pdfeng "github.com/seiortech/mtools-plugin-pdf/internal/pdf"
	"github.com/seiortech/mtools-sdk/i18n"
	"github.com/seiortech/mtools-sdk/job"
	"github.com/seiortech/mtools-sdk/pluginapi"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

//go:embed locales/*.json
var localesFS embed.FS

const (
	defaultPageW     = 595.0
	defaultPageH     = 842.0
	minZoomFactor    = 0.25
	maxZoomFactor    = 3.0
	defaultRailWidth = 320.0
)

type pageSize struct {
	W float64
	H float64
}

type pageAssetKey struct {
	sourcePath string
	docGen     uint64
	pageNum    int
	scale1000  int
}

type previewPage struct {
	Kind       string // "pdf" or "image"
	SourcePath string
	SourceName string
	SourceIdx  int
	PDFPage    int
	GlobalPage int
	W, H       float64
	ImageAsset string
}

type liveFileItem struct {
	Path      string
	Name      string
	SizeStr   string
	PageCount int
	PageSizes []pageSize
	AssetRef  string
	AssetFile string
	ImgW      float64
	ImgH      float64
}

// LiveUI implements pluginapi.LiveUI for PDF Utility as a full-bleed
// Sage Garden workbench with native PDF & image page previews.
type LiveUI struct {
	mu               sync.Mutex
	renderMu         sync.Mutex
	rev              uint64
	catalog          *i18n.Catalog
	lang             i18n.Lang
	dataDir          string
	appSettingsFile  string
	sharedPDFiumDir  string
	history          *pdfeng.HistoryStore
	renderer         pdfeng.PageRenderer
	previewEngineErr error

	activeTab string // "merge", "split", "images", "office"

	// Merge
	mergeFiles      []liveFileItem
	mergeOutputName string

	// Split / Extract
	splitFile       *liveFileItem
	splitMode       string // "all" or "range"
	splitSpan       string
	splitPageRange  string
	splitOutputName string

	// Images to PDF
	imageFiles      []liveFileItem
	imageOutputName string

	// Office to PDF
	officeFiles        []liveFileItem
	officePreviewFiles []liveFileItem
	sofficePath        string

	// Preview & viewport state
	docGen             uint64
	previewPages       []previewPage
	currentPage        int
	zoom               float64
	fitZoom            float64
	viewportW          float64
	viewportH          float64
	viewportFromScroll bool
	scrollY            float64
	targetScrollY      float64
	scrollSeq          uint64
	railWidth          float32
	railCollapsed      bool

	// Page bitmap & asset cache
	pageCache       *pdfeng.PageBitmapCache
	pageAssets      map[pageAssetKey]string
	pageAssetFiles  map[string]string
	renderSem        chan struct{}
	inFlightRenders  map[pageAssetKey]bool
	previewRenderErr map[pageAssetKey]string
	zoomDebounce     *time.Timer
	assetSeq        int

	// Output & job state
	outputDir        string
	inlineError      string
	running          bool
	progress         float64
	progressMsg      string
	resultMsg        string
	resultOutputs    []string
	resultAuthorized bool
	jobCancel        context.CancelFunc
	jobGen           uint64
}

// NewLiveUI creates a LiveUI instance for PDF Utility.
func NewLiveUI() *LiveUI {
	cat, _ := i18n.NewCatalogFromFS(localesFS, "locales")
	return &LiveUI{
		catalog:         cat,
		lang:            i18n.LangEN,
		renderer:        pdfeng.DefaultRenderer(),
		activeTab:       "merge",
		mergeOutputName: "merged.pdf",
		splitMode:       "all",
		splitSpan:       "1",
		splitOutputName: "extracted.pdf",
		imageOutputName: "images.pdf",
		currentPage:     1,
		zoom:            1.0,
		fitZoom:         1.0,
		railWidth:       defaultRailWidth,
		pageCache:       pdfeng.NewPageBitmapCache(48),
		pageAssets:      make(map[pageAssetKey]string),
		pageAssetFiles:  make(map[string]string),
		renderSem:        make(chan struct{}, 2),
		inFlightRenders:  make(map[pageAssetKey]bool),
		previewRenderErr: make(map[pageAssetKey]string),
	}
}

func (u *LiveUI) t(key, fallback string) string {
	if u.catalog == nil {
		return fallback
	}
	v := u.catalog.T(u.lang, key)
	if v == "" || v == key {
		return fallback
	}
	return v
}

func (u *LiveUI) tf(key string, args ...any) string {
	if u.catalog == nil {
		return fmt.Sprintf(key, args...)
	}
	return u.catalog.Tf(u.lang, key, args...)
}

func (u *LiveUI) Open(ctx context.Context, params pluginapi.UIOpenParams, session *pluginapi.UISession) (pluginapi.UITree, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if params.Lang != "" {
		u.lang = i18n.NormalizeLang(params.Lang)
	}
	if params.Viewport.Width > 0 {
		u.viewportW = float64(params.Viewport.Width)
	}
	if params.Viewport.Height > 0 {
		u.viewportH = float64(params.Viewport.Height)
	}
	if dir := params.Settings["output_dir"]; dir != "" {
		u.outputDir = expandHome(dir)
	}
	if u.outputDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			u.outputDir = filepath.Join(home, "Downloads")
		}
	}
	if sp := params.Settings["soffice_path"]; sp != "" {
		u.sofficePath = sp
	} else if sp := params.Settings["helper_soffice"]; sp != "" {
		u.sofficePath = sp
	}
	if u.dataDir == "" {
		if dd := params.Settings["plugin_data_dir"]; dd != "" {
			u.dataDir = dd
			u.history = pdfeng.NewHistoryStore(dd)
		} else if dd := os.Getenv("MTOOLS_PLUGIN_DATA"); dd != "" {
			u.dataDir = dd
			u.history = pdfeng.NewHistoryStore(dd)
		}
	}
	u.appSettingsFile = params.Settings["app_settings_file"]
	u.sharedPDFiumDir = params.Settings["shared_pdfium_dir"]
	u.configureRendererLocked(params.Settings)
	if ec, ok := u.renderer.(interface{ CheckEngine() error }); ok {
		u.previewEngineErr = ec.CheckEngine()
	}

	return u.buildTreeLocked(), nil
}

func (u *LiveUI) configureRendererLocked(settings map[string]string) {
	cr, ok := u.renderer.(pdfeng.ConfigurableRenderer)
	if !ok {
		return
	}
	wasmPath := ""
	if settings != nil {
		wasmPath = strings.TrimSpace(settings["pdfium_wasm_path"])
	}
	if wasmPath == "" && u.appSettingsFile != "" {
		if raw, err := os.ReadFile(u.appSettingsFile); err == nil {
			var parsed map[string]any
			if json.Unmarshal(raw, &parsed) == nil {
				if v, ok := parsed["pdfium_wasm_path"].(string); ok {
					wasmPath = strings.TrimSpace(v)
				}
			}
		}
	}
	if wasmPath == "" && u.sharedPDFiumDir != "" {
		candidate := filepath.Join(u.sharedPDFiumDir, "pdfium.wasm")
		if _, err := os.Stat(candidate); err == nil {
			wasmPath = candidate
		}
	}
	cacheDir := ""
	if u.dataDir != "" {
		cacheDir = filepath.Join(u.dataDir, "pdfium-cache")
	}
	cr.Configure(pdfeng.RendererConfig{
		WASMPath: wasmPath,
		CacheDir: cacheDir,
	})
}

func (u *LiveUI) Close(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.zoomDebounce != nil {
		u.zoomDebounce.Stop()
		u.zoomDebounce = nil
	}
	if u.jobCancel != nil {
		u.jobCancel()
		u.jobCancel = nil
	}
	if cr, ok := u.renderer.(pdfeng.ConfigurableRenderer); ok {
		return cr.Close()
	}
	return nil
}

func (u *LiveUI) Event(ctx context.Context, ev pluginapi.UIEvent, session *pluginapi.UISession) error {
	u.mu.Lock()
	shouldRender := true
	switch ev.Node {
	case "retry_pdfium":
		if ev.Kind == pluginapi.UIEventClick {
			u.previewEngineErr = nil
			u.previewRenderErr = make(map[pageAssetKey]string)
			u.inFlightRenders = make(map[pageAssetKey]bool)
			u.configureRendererLocked(nil)
			if ec, ok := u.renderer.(interface{ CheckEngine() error }); ok {
				u.previewEngineErr = ec.CheckEngine()
			}
			u.pageCache.Clear()
			u.clearPageAssetsLocked(session)
			u.triggerVisiblePageRendersLocked(session)
		} else {
			shouldRender = false
		}
	case "pdf.tabs":
		if ev.Kind == pluginapi.UIEventSelect && ev.Value != "" && ev.Value != u.activeTab {
			u.activeTab = ev.Value
			u.inlineError = ""
			u.resultMsg = ""
			u.resultOutputs = nil
			u.resultAuthorized = false
			u.rebuildPreviewLocked(session, true)
		}
	case "pdf.main_split":
		if ev.Kind == pluginapi.UIEventResize {
			if w, err := strconv.Atoi(strings.TrimSpace(ev.Value)); err == nil && w >= 240 && w <= 560 {
				u.railWidth = float32(w)
				shouldRender = false
			}
		}
	case "pdf.rail_toggle", "pdf.rail_expand":
		if ev.Kind == pluginapi.UIEventClick {
			u.railCollapsed = !u.railCollapsed
		}
	case "pdf.viewport":
		switch ev.Kind {
		case pluginapi.UIEventScroll:
			shouldRender = u.handleViewportScrollLocked(ev.Value, session)
		case pluginapi.UIEventZoom:
			shouldRender = u.handleViewportZoomLocked(ev.Value, session)
		default:
			shouldRender = false
		}
	case "pdf.page_prev":
		if ev.Kind == pluginapi.UIEventClick && u.currentPage > 1 {
			u.jumpToPageLocked(u.currentPage-1, session)
		}
	case "pdf.page_next":
		if ev.Kind == pluginapi.UIEventClick && u.currentPage < len(u.previewPages) {
			u.jumpToPageLocked(u.currentPage+1, session)
		}
	case "pdf.zoom_out":
		if ev.Kind == pluginapi.UIEventClick {
			u.setZoomLocked(u.effectiveZoomLocked()/1.2, session)
		}
	case "pdf.zoom_in":
		if ev.Kind == pluginapi.UIEventClick {
			u.setZoomLocked(u.effectiveZoomLocked()*1.2, session)
		}
	case "pdf.zoom_fit":
		if ev.Kind == pluginapi.UIEventClick {
			u.fitZoomLocked(session)
		}
	case "pdf.merge.pick", "pdf.merge.empty_pick":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			for _, p := range splitPickedPaths(ev.Value) {
				u.mergeFiles = append(u.mergeFiles, u.makeItemLocked(ctx, p, session, false))
			}
			u.inlineError = ""
			u.resultMsg = ""
			u.rebuildPreviewLocked(session, false)
		}
	case "pdf.merge.clear":
		if ev.Kind == pluginapi.UIEventClick && !u.running {
			u.mergeFiles = nil
			u.inlineError = ""
			u.resultMsg = ""
			u.rebuildPreviewLocked(session, true)
		}
	case "pdf.merge.outname":
		if ev.Kind == pluginapi.UIEventChange {
			u.mergeOutputName = ev.Value
			shouldRender = false
		}
	case "pdf.split.pick", "pdf.split.empty_pick":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			item := u.makeItemLocked(ctx, ev.Value, session, false)
			u.splitFile = &item
			u.inlineError = ""
			u.resultMsg = ""
			u.rebuildPreviewLocked(session, true)
		}
	case "pdf.split.clear":
		if ev.Kind == pluginapi.UIEventClick && !u.running {
			u.splitFile = nil
			u.splitPageRange = ""
			u.inlineError = ""
			u.resultMsg = ""
			u.rebuildPreviewLocked(session, true)
		}
	case "pdf.split.mode":
		if ev.Kind == pluginapi.UIEventSelect && ev.Value != "" {
			u.splitMode = ev.Value
			u.inlineError = ""
		}
	case "pdf.split.span":
		if ev.Kind == pluginapi.UIEventChange {
			u.splitSpan = ev.Value
		}
	case "pdf.split.range":
		if ev.Kind == pluginapi.UIEventChange {
			u.splitPageRange = ev.Value
		}
	case "pdf.split.outname":
		if ev.Kind == pluginapi.UIEventChange {
			u.splitOutputName = ev.Value
			shouldRender = false
		}
	case "pdf.images.pick", "pdf.images.empty_pick":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			for _, p := range splitPickedPaths(ev.Value) {
				u.imageFiles = append(u.imageFiles, u.makeItemLocked(ctx, p, session, true))
			}
			u.inlineError = ""
			u.resultMsg = ""
			u.rebuildPreviewLocked(session, false)
		}
	case "pdf.images.clear":
		if ev.Kind == pluginapi.UIEventClick && !u.running {
			u.clearImageAssetsLocked(session)
			u.imageFiles = nil
			u.inlineError = ""
			u.resultMsg = ""
			u.rebuildPreviewLocked(session, true)
		}
	case "pdf.images.outname":
		if ev.Kind == pluginapi.UIEventChange {
			u.imageOutputName = ev.Value
			shouldRender = false
		}
	case "pdf.office.pick", "pdf.office.empty_pick":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			for _, p := range splitPickedPaths(ev.Value) {
				u.officeFiles = append(u.officeFiles, u.makeItemLocked(ctx, p, session, false))
			}
			u.officePreviewFiles = nil
			u.inlineError = ""
			u.resultMsg = ""
			u.rebuildPreviewLocked(session, true)
		}
	case "pdf.office.clear":
		if ev.Kind == pluginapi.UIEventClick && !u.running {
			u.officeFiles = nil
			u.officePreviewFiles = nil
			u.inlineError = ""
			u.resultMsg = ""
			u.rebuildPreviewLocked(session, true)
		}
	case "pdf.outdir":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			u.outputDir = ev.Value
		}
	case "pdf.run":
		if !u.running {
			switch ev.Kind {
			case pluginapi.UIEventChange:
				if ev.Value != "" {
					u.startJobLocked(ctx, session, ev.Value, true)
				}
			case pluginapi.UIEventClick:
				u.startJobLocked(ctx, session, "", false)
			}
		}
	case "pdf.cancel":
		if ev.Kind == pluginapi.UIEventClick && u.running && u.jobCancel != nil {
			u.jobCancel()
			u.progressMsg = u.t("pdf.canceled", "Canceling...")
		}
	case "pdf.dismiss_banner":
		if ev.Kind == pluginapi.UIEventClick {
			u.inlineError = ""
			u.resultMsg = ""
			u.resultOutputs = nil
			u.resultAuthorized = false
		}
	case "pdf.root":
		if ev.Kind == pluginapi.UIEventShortcut {
			shouldRender = u.handleShortcutLocked(ev.Value, session)
		} else {
			shouldRender = false
		}
	default:
		shouldRender = u.handleDynamicEventLocked(ev, session)
	}

	if !shouldRender {
		u.mu.Unlock()
		return nil
	}
	u.mu.Unlock()

	return u.renderAndSend(session)
}

func (u *LiveUI) renderAndSend(session *pluginapi.UISession) error {
	if session == nil {
		return nil
	}
	u.renderMu.Lock()
	defer u.renderMu.Unlock()

	u.mu.Lock()
	tree := u.buildTreeLocked()
	u.mu.Unlock()

	return session.Render(tree)
}

func (u *LiveUI) handleShortcutLocked(val string, session *pluginapi.UISession) bool {
	switch val {
	case "ArrowLeft":
		if u.currentPage > 1 {
			u.jumpToPageLocked(u.currentPage-1, session)
			return true
		}
	case "ArrowRight":
		if u.currentPage < len(u.previewPages) {
			u.jumpToPageLocked(u.currentPage+1, session)
			return true
		}
	case "=":
		u.setZoomLocked(u.effectiveZoomLocked()*1.2, session)
		return true
	case "-":
		u.setZoomLocked(u.effectiveZoomLocked()/1.2, session)
		return true
	case "0":
		u.fitZoomLocked(session)
		return true
	case "Escape":
		if u.inlineError != "" || u.resultMsg != "" {
			u.inlineError = ""
			u.resultMsg = ""
			return true
		}
	}
	return false
}

func (u *LiveUI) handleDynamicEventLocked(ev pluginapi.UIEvent, session *pluginapi.UISession) bool {
	if ev.Kind != pluginapi.UIEventClick {
		return false
	}
	if strings.HasPrefix(ev.Node, "rail_page_") {
		if p, err := strconv.Atoi(strings.TrimPrefix(ev.Node, "rail_page_")); err == nil {
			u.jumpToPageLocked(p, session)
			return true
		}
		return false
	}
	if strings.HasPrefix(ev.Node, "page_") {
		if p, err := strconv.Atoi(strings.TrimPrefix(ev.Node, "page_")); err == nil {
			u.currentPage = p
			if u.activeTab == "split" && u.splitMode == "range" && len(u.previewPages) > 0 {
				u.splitPageRange = togglePageInRange(u.splitPageRange, p, len(u.previewPages))
			}
			return true
		}
		return false
	}

	var list *[]liveFileItem
	var prefix string
	isImageList := false
	switch {
	case strings.HasPrefix(ev.Node, "pdf.merge.item."):
		list = &u.mergeFiles
		prefix = "pdf.merge.item."
	case strings.HasPrefix(ev.Node, "pdf.images.item."):
		list = &u.imageFiles
		prefix = "pdf.images.item."
		isImageList = true
	case strings.HasPrefix(ev.Node, "pdf.office.item."):
		list = &u.officeFiles
		prefix = "pdf.office.item."
	default:
		return false
	}

	rest := strings.TrimPrefix(ev.Node, prefix)
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 {
		return false
	}
	idx, err := strconv.Atoi(parts[0])
	if err != nil || idx < 0 || idx >= len(*list) {
		return false
	}
	s := *list
	switch parts[1] {
	case "jump":
		u.jumpToSourceIndexLocked(idx, session)
		return true
	case "up":
		if idx > 0 {
			s[idx-1], s[idx] = s[idx], s[idx-1]
			u.rebuildPreviewLocked(session, false)
			return true
		}
	case "down":
		if idx+1 < len(s) {
			s[idx+1], s[idx] = s[idx], s[idx+1]
			u.rebuildPreviewLocked(session, false)
			return true
		}
	case "rm":
		if isImageList && session != nil && session.AssetsDir() != "" && s[idx].AssetFile != "" {
			_ = os.Remove(filepath.Join(session.AssetsDir(), s[idx].AssetFile))
		}
		*list = append(s[:idx], s[idx+1:]...)
		if u.activeTab == "office" {
			u.officePreviewFiles = nil
		}
		u.rebuildPreviewLocked(session, false)
		return true
	}
	return false
}

func (u *LiveUI) makeItemLocked(ctx context.Context, path string, session *pluginapi.UISession, copyImageAsset bool) liveFileItem {
	info, err := os.Stat(path)
	sizeStr := ""
	if err == nil {
		sizeStr = formatLiveFileSize(info.Size())
	}
	item := liveFileItem{
		Path:      path,
		Name:      filepath.Base(path),
		SizeStr:   sizeStr,
		PageCount: -1,
	}
	if strings.EqualFold(filepath.Ext(path), ".pdf") && u.renderer != nil {
		if cnt, err := u.renderer.PageCount(ctx, path); err == nil && cnt > 0 {
			item.PageCount = cnt
			item.PageSizes = make([]pageSize, cnt)
			for p := 1; p <= cnt; p++ {
				w, h, err := u.renderer.PageDimensions(ctx, path, p)
				if err != nil || w <= 0 || h <= 0 {
					w, h = defaultPageW, defaultPageH
				}
				item.PageSizes[p-1] = pageSize{W: w, H: h}
			}
		}
	}
	if copyImageAsset {
		if f, err := os.Open(path); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil && cfg.Width > 0 && cfg.Height > 0 {
				item.ImgW = float64(cfg.Width)
				item.ImgH = float64(cfg.Height)
			}
			_ = f.Close()
		}
		if session != nil && session.AssetsDir() != "" {
			if data, err := os.ReadFile(path); err == nil && len(data) <= 8<<20 {
				u.assetSeq++
				name := fmt.Sprintf("img_%d%s", u.assetSeq, strings.ToLower(filepath.Ext(path)))
				dst := filepath.Join(session.AssetsDir(), name)
				if os.WriteFile(dst, data, 0600) == nil {
					item.AssetRef = "asset:" + name
					item.AssetFile = name
				}
			}
		}
	}
	return item
}

func (u *LiveUI) rebuildPreviewLocked(session *pluginapi.UISession, resetScroll bool) {
	u.docGen++
	u.clearPageAssetsLocked(session)
	u.previewPages = nil

	var pages []previewPage
	addPDFFiles := func(items []liveFileItem) {
		for sIdx, it := range items {
			if it.PageCount <= 0 {
				continue
			}
			for p := 1; p <= it.PageCount; p++ {
				dim := pageSize{W: defaultPageW, H: defaultPageH}
				if p-1 < len(it.PageSizes) {
					dim = it.PageSizes[p-1]
				}
				pages = append(pages, previewPage{
					Kind:       "pdf",
					SourcePath: it.Path,
					SourceName: it.Name,
					SourceIdx:  sIdx,
					PDFPage:    p,
					GlobalPage: len(pages) + 1,
					W:          dim.W,
					H:          dim.H,
				})
			}
		}
	}

	switch u.activeTab {
	case "merge":
		addPDFFiles(u.mergeFiles)
	case "split":
		if u.splitFile != nil {
			addPDFFiles([]liveFileItem{*u.splitFile})
		}
	case "images":
		for idx, it := range u.imageFiles {
			w, h := defaultPageW, defaultPageH
			if it.ImgW > 0 && it.ImgH > 0 {
				w = defaultPageW
				h = defaultPageW * (it.ImgH / it.ImgW)
				if h < 180 {
					h = 180
				}
				if h > 1100 {
					h = 1100
				}
			}
			pages = append(pages, previewPage{
				Kind:       "image",
				SourcePath: it.Path,
				SourceName: it.Name,
				SourceIdx:  idx,
				PDFPage:    1,
				GlobalPage: len(pages) + 1,
				W:          w,
				H:          h,
				ImageAsset: it.AssetRef,
			})
		}
	case "office":
		if len(u.officePreviewFiles) > 0 {
			addPDFFiles(u.officePreviewFiles)
		}
	}

	u.previewPages = pages
	if len(pages) == 0 {
		u.currentPage = 1
		u.scrollY = 0
		u.targetScrollY = 0
		return
	}
	if resetScroll || u.currentPage < 1 || u.currentPage > len(pages) {
		u.currentPage = 1
		u.scrollY = 0
		u.targetScrollY = 0
		u.scrollSeq++
	}
	if resetScroll {
		u.computeFitZoomLocked()
		u.zoom = u.fitZoom
	}
	u.triggerVisiblePageRendersLocked(session)
}

func (u *LiveUI) computeFitZoomLocked() {
	if len(u.previewPages) == 0 {
		u.fitZoom = 1.0
		return
	}
	first := u.previewPages[0]
	railW := float64(u.railWidth)
	if u.railCollapsed {
		railW = 48
	}
	vpW := u.viewportW
	if vpW <= 0 {
		vpW = 900
	}
	availW := vpW - 64
	if !u.viewportFromScroll {
		availW -= railW
	}
	if availW < 320 {
		availW = 500
	}
	z := availW / first.W
	if z < 0.45 {
		z = 0.45
	} else if z > 1.6 {
		z = 1.6
	}
	u.fitZoom = z
}

func (u *LiveUI) fitZoomLocked(session *pluginapi.UISession) {
	u.computeFitZoomLocked()
	u.setZoomLocked(u.fitZoom, session)
}

func (u *LiveUI) setZoomLocked(z float64, session *pluginapi.UISession) {
	if z < minZoomFactor {
		z = minZoomFactor
	} else if z > maxZoomFactor {
		z = maxZoomFactor
	}
	if math.Abs(z-u.zoom) < 0.002 {
		return
	}
	u.zoom = z
	if len(u.previewPages) == 0 {
		return
	}
	if u.zoomDebounce != nil {
		u.zoomDebounce.Stop()
	}
	docGen := u.docGen
	u.zoomDebounce = time.AfterFunc(100*time.Millisecond, func() {
		u.mu.Lock()
		if u.docGen != docGen || len(u.previewPages) == 0 {
			u.mu.Unlock()
			return
		}
		u.triggerVisiblePageRendersLocked(session)
		u.mu.Unlock()
	})
}

func (u *LiveUI) effectiveZoomLocked() float64 {
	if u.zoom <= 0 {
		return 1.0
	}
	return u.zoom
}

func (u *LiveUI) pageTopYLocked(globalPage int) float64 {
	const pageGap = 24.0
	const topPad = 24.0
	y := topPad
	z := u.effectiveZoomLocked()
	for i := 1; i < globalPage && i <= len(u.previewPages); i++ {
		y += u.previewPages[i-1].H*z + pageGap
	}
	return y
}

func (u *LiveUI) jumpToPageLocked(globalPage int, session *pluginapi.UISession) {
	if len(u.previewPages) == 0 {
		return
	}
	if globalPage < 1 {
		globalPage = 1
	} else if globalPage > len(u.previewPages) {
		globalPage = len(u.previewPages)
	}
	u.currentPage = globalPage
	targetY := u.pageTopYLocked(globalPage) - 24
	if targetY < 0 {
		targetY = 0
	}
	u.scrollSeq++
	u.targetScrollY = targetY
	u.triggerVisiblePageRendersLocked(session)
}

func (u *LiveUI) jumpToSourceIndexLocked(sourceIdx int, session *pluginapi.UISession) {
	for _, pg := range u.previewPages {
		if pg.SourceIdx == sourceIdx {
			u.jumpToPageLocked(pg.GlobalPage, session)
			return
		}
	}
}

func (u *LiveUI) handleViewportScrollLocked(val string, session *pluginapi.UISession) bool {
	parts := strings.Split(val, ",")
	if len(parts) < 4 {
		return false
	}
	sy, e2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	cw, e3 := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	ch, e4 := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
	if e2 != nil {
		return false
	}
	if e3 == nil && cw > 0 {
		u.viewportW = cw
		u.viewportFromScroll = true
	}
	if e4 == nil && ch > 0 {
		u.viewportH = ch
	}
	u.scrollY = sy

	if len(u.previewPages) <= 1 {
		return false
	}
	focusY := sy + 200
	if focusY < 0 {
		focusY = 0
	}
	const pageGap = 24.0
	const topPad = 24.0
	z := u.effectiveZoomLocked()
	currY := topPad
	found := 1
	for i := 1; i <= len(u.previewPages); i++ {
		ph := u.previewPages[i-1].H * z
		if focusY >= currY && focusY <= currY+ph+pageGap {
			found = i
			break
		}
		if focusY > currY+ph {
			found = i
		}
		currY += ph + pageGap
	}
	if found != u.currentPage {
		u.currentPage = found
		u.triggerVisiblePageRendersLocked(session)
		return true
	}
	return false
}

func (u *LiveUI) handleViewportZoomLocked(val string, session *pluginapi.UISession) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
	if err != nil || f == 0 {
		return false
	}
	nextZoom := u.effectiveZoomLocked() * (1 + f*0.02)
	u.setZoomLocked(nextZoom, session)
	return true
}

func (u *LiveUI) triggerVisiblePageRendersLocked(session *pluginapi.UISession) {
	if len(u.previewPages) == 0 {
		return
	}
	scale := pdfeng.QuantizeRenderScale(u.effectiveZoomLocked())
	start := u.currentPage - 2
	if start < 1 {
		start = 1
	}
	end := u.currentPage + 2
	if end > len(u.previewPages) {
		end = len(u.previewPages)
	}
	for p := start; p <= end; p++ {
		pg := u.previewPages[p-1]
		if pg.Kind == "pdf" {
			u.requestPageRenderLocked(pg.SourcePath, pg.PDFPage, scale, session)
		}
	}
}

func (u *LiveUI) requestPageRenderLocked(pdfPath string, pdfPage int, scale float64, session *pluginapi.UISession) {
	if u.renderer == nil || pdfPath == "" || pdfPage < 1 {
		return
	}
	scale1000 := int(math.Round(scale * 1000))
	key := pageAssetKey{
		sourcePath: pdfPath,
		docGen:     u.docGen,
		pageNum:    pdfPage,
		scale1000:  scale1000,
	}
	if _, ok := u.pageAssets[key]; ok {
		return
	}
	if u.inFlightRenders[key] {
		return
	}
	if cachedImg, ok := u.pageCache.Get(pdfPath, pdfPage, scale); ok {
		u.storeRenderedPageAssetLocked(key, cachedImg, session)
		return
	}

	u.inFlightRenders[key] = true
	docGen := u.docGen
	renderer := u.renderer
	sem := u.renderSem
	go func() {
		sem <- struct{}{}
		defer func() { <-sem }()

		img, err := renderer.RenderPage(context.Background(), pdfPath, pdfPage, scale)
		if err != nil {
			u.mu.Lock()
			delete(u.inFlightRenders, key)
			if u.docGen != docGen {
				u.mu.Unlock()
				return
			}
			if errors.Is(err, pdfeng.ErrPDFiumMissing) || errors.Is(err, pdfeng.ErrPDFiumInvalid) || errors.Is(err, pdfeng.ErrNoPreviewEngine) {
				u.previewEngineErr = err
			} else {
				u.previewRenderErr[key] = err.Error()
			}
			u.mu.Unlock()
			_ = u.renderAndSend(session)
			return
		}
		if img == nil {
			u.mu.Lock()
			delete(u.inFlightRenders, key)
			u.mu.Unlock()
			return
		}

		var pngBytes []byte
		if session != nil && session.AssetsDir() != "" {
			pngBytes, _ = encodeAssetPNG(img)
		}

		u.mu.Lock()
		delete(u.inFlightRenders, key)
		if u.docGen != docGen {
			u.mu.Unlock()
			return
		}
		u.previewEngineErr = nil
		delete(u.previewRenderErr, key)
		u.pageCache.Put(pdfPath, pdfPage, scale, img)
		if len(pngBytes) > 0 {
			u.storeRenderedPageAssetBytesLocked(key, pngBytes, session)
		} else {
			u.storeRenderedPageAssetLocked(key, img, session)
		}
		u.mu.Unlock()

		_ = u.renderAndSend(session)
	}()
}

func (u *LiveUI) storeRenderedPageAssetLocked(key pageAssetKey, img image.Image, session *pluginapi.UISession) {
	if session == nil || session.AssetsDir() == "" || img == nil {
		return
	}
	b, err := encodeAssetPNG(img)
	if err != nil {
		return
	}
	u.storeRenderedPageAssetBytesLocked(key, b, session)
}

func (u *LiveUI) storeRenderedPageAssetBytesLocked(key pageAssetKey, pngBytes []byte, session *pluginapi.UISession) {
	if session == nil || session.AssetsDir() == "" || len(pngBytes) == 0 {
		return
	}
	u.assetSeq++
	fileName := fmt.Sprintf("page-%d-g%d-s%d-v%d.png", key.pageNum, key.docGen, key.scale1000, u.assetSeq)
	if err := writeAssetPNGBytes(session.AssetsDir(), fileName, pngBytes); err != nil {
		return
	}
	ref := "asset:" + fileName
	u.pageAssets[key] = ref
	u.pageAssetFiles[ref] = fileName
}

func (u *LiveUI) pageAssetRefLocked(pg previewPage) string {
	if pg.Kind == "image" {
		return pg.ImageAsset
	}
	scale := pdfeng.QuantizeRenderScale(u.effectiveZoomLocked())
	exactKey := pageAssetKey{
		sourcePath: pg.SourcePath,
		docGen:     u.docGen,
		pageNum:    pg.PDFPage,
		scale1000:  int(math.Round(scale * 1000)),
	}
	if ref, ok := u.pageAssets[exactKey]; ok {
		return ref
	}
	bestDist := math.MaxFloat64
	bestRef := ""
	target := int(math.Round(scale * 1000))
	for k, ref := range u.pageAssets {
		if k.docGen != u.docGen || k.sourcePath != pg.SourcePath || k.pageNum != pg.PDFPage {
			continue
		}
		d := math.Abs(float64(k.scale1000 - target))
		if d < bestDist {
			bestDist = d
			bestRef = ref
		}
	}
	return bestRef
}

func (u *LiveUI) clearPageAssetsLocked(session *pluginapi.UISession) {
	if session != nil && session.AssetsDir() != "" {
		dir := session.AssetsDir()
		for _, fn := range u.pageAssetFiles {
			_ = os.Remove(filepath.Join(dir, fn))
		}
	}
	u.pageAssets = make(map[pageAssetKey]string)
	u.pageAssetFiles = make(map[string]string)
	u.previewRenderErr = make(map[pageAssetKey]string)
}

func (u *LiveUI) clearImageAssetsLocked(session *pluginapi.UISession) {
	if session != nil && session.AssetsDir() != "" {
		dir := session.AssetsDir()
		for _, it := range u.imageFiles {
			if it.AssetFile != "" {
				_ = os.Remove(filepath.Join(dir, it.AssetFile))
			}
		}
	}
}

func (u *LiveUI) defaultOutputPathLocked() string {
	switch u.activeTab {
	case "merge":
		return filepath.Join(u.outputDir, normalizePDFName(u.mergeOutputName, "merged.pdf"))
	case "split":
		return filepath.Join(u.outputDir, normalizePDFName(u.splitOutputName, "extracted.pdf"))
	case "images":
		return filepath.Join(u.outputDir, normalizePDFName(u.imageOutputName, "images.pdf"))
	default:
		return u.outputDir
	}
}

func (u *LiveUI) canRunLocked() bool {
	if u.running {
		return false
	}
	switch u.activeTab {
	case "merge":
		return len(u.mergeFiles) >= 2
	case "split":
		if u.splitFile == nil || u.splitFile.Path == "" {
			return false
		}
		if u.splitMode == "range" {
			return len(parsePageRanges(u.splitPageRange)) > 0
		}
		return true
	case "images":
		return len(u.imageFiles) >= 1
	case "office":
		return len(u.officeFiles) >= 1
	default:
		return false
	}
}

func (u *LiveUI) startJobLocked(ctx context.Context, session *pluginapi.UISession, explicitOutFile string, authorized bool) {
	var pdfJob pdfeng.PDFJob
	switch u.activeTab {
	case "merge":
		if len(u.mergeFiles) < 2 {
			u.inlineError = "Select at least two PDF files to merge."
			return
		}
		outFile := explicitOutFile
		if outFile == "" {
			outFile = filepath.Join(u.outputDir, normalizePDFName(u.mergeOutputName, "merged.pdf"))
		}
		inFiles := make([]string, len(u.mergeFiles))
		for i, f := range u.mergeFiles {
			inFiles[i] = f.Path
		}
		pdfJob = pdfeng.PDFJob{
			Op:         pdfeng.OpMerge,
			InputFiles: inFiles,
			OutputFile: outFile,
		}
	case "split":
		if u.splitFile == nil || u.splitFile.Path == "" {
			u.inlineError = "Select a PDF file first."
			return
		}
		if u.splitMode == "range" {
			ranges := parsePageRanges(u.splitPageRange)
			if len(ranges) == 0 {
				u.inlineError = "Enter a valid page selection (e.g. 1-3, 5)."
				return
			}
			outFile := explicitOutFile
			if outFile == "" {
				outFile = filepath.Join(u.outputDir, normalizePDFName(u.splitOutputName, "extracted.pdf"))
			}
			pdfJob = pdfeng.PDFJob{
				Op:         pdfeng.OpExtract,
				InputFiles: []string{u.splitFile.Path},
				OutputFile: outFile,
				PageRanges: ranges,
			}
		} else {
			span := 1
			if n, err := strconv.Atoi(strings.TrimSpace(u.splitSpan)); err == nil && n > 0 {
				span = n
			}
			pdfJob = pdfeng.PDFJob{
				Op:         pdfeng.OpSplit,
				InputFiles: []string{u.splitFile.Path},
				OutputDir:  u.outputDir,
				PageSpan:   span,
			}
		}
	case "images":
		if len(u.imageFiles) == 0 {
			u.inlineError = "Select at least one image."
			return
		}
		outFile := explicitOutFile
		if outFile == "" {
			outFile = filepath.Join(u.outputDir, normalizePDFName(u.imageOutputName, "images.pdf"))
		}
		inFiles := make([]string, len(u.imageFiles))
		for i, f := range u.imageFiles {
			inFiles[i] = f.Path
		}
		pdfJob = pdfeng.PDFJob{
			Op:         pdfeng.OpImages,
			InputFiles: inFiles,
			OutputFile: outFile,
		}
	case "office":
		if len(u.officeFiles) == 0 {
			u.inlineError = "Select at least one Office document."
			return
		}
		inFiles := make([]string, len(u.officeFiles))
		for i, f := range u.officeFiles {
			inFiles[i] = f.Path
		}
		pdfJob = pdfeng.PDFJob{
			Op:          pdfeng.OpOffice,
			InputFiles:  inFiles,
			OutputDir:   u.outputDir,
			SofficePath: u.sofficePath,
		}
	default:
		return
	}

	jobCtx, cancel := context.WithCancel(ctx)
	u.jobGen++
	curGen := u.jobGen
	u.jobCancel = cancel
	u.running = true
	u.inlineError = ""
	u.resultMsg = ""
	u.resultOutputs = nil
	u.resultAuthorized = authorized
	u.progress = 5
	u.progressMsg = "Starting…"

	go func() {
		defer cancel()
		res, err := pdfJob.Run(jobCtx, func(pr job.Progress) {
			u.mu.Lock()
			if u.jobGen != curGen {
				u.mu.Unlock()
				return
			}
			u.progress = pr.Done
			u.progressMsg = pr.Message
			u.mu.Unlock()
			_ = u.renderAndSend(session)
		})

		u.mu.Lock()
		if u.jobGen != curGen {
			u.mu.Unlock()
			return
		}
		u.running = false
		u.jobCancel = nil
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, job.ErrCanceled) {
				u.inlineError = u.t("pdf.canceled", "Operation canceled.")
			} else {
				u.inlineError = err.Error()
			}
			u.progress = 0
			u.progressMsg = ""
		} else {
			u.progress = 100
			u.resultOutputs = res.Outputs
			u.resultMsg = res.Summary
			if u.resultMsg == "" {
				u.resultMsg = u.t("pdf.complete", "Completed.")
			}
			if u.history != nil && len(res.Outputs) > 0 {
				_ = u.history.Add(pdfeng.HistoryItem{
					ID:          fmt.Sprintf("pdf-%d", time.Now().UnixNano()),
					Operation:   string(pdfJob.Op),
					Summary:     u.resultMsg,
					OutputFile:  res.Outputs[0],
					FileCount:   len(pdfJob.InputFiles),
					CompletedAt: time.Now(),
				})
			}
			if pdfJob.Op == pdfeng.OpOffice && len(res.Outputs) > 0 {
				var converted []liveFileItem
				for _, outPDF := range res.Outputs {
					converted = append(converted, u.makeItemLocked(context.Background(), outPDF, session, false))
				}
				u.officePreviewFiles = converted
				u.rebuildPreviewLocked(session, true)
			}
		}
		u.mu.Unlock()
		_ = u.renderAndSend(session)
	}()
}

func liveNode(id, kind string, props any, children ...pluginapi.UINode) pluginapi.UINode {
	var rawMap map[string]json.RawMessage
	if props != nil {
		if b, err := json.Marshal(props); err == nil {
			_ = json.Unmarshal(b, &rawMap)
		}
	}
	return pluginapi.UINode{
		ID:       id,
		Kind:     kind,
		Props:    rawMap,
		Children: children,
	}
}

func encodeAssetPNG(img image.Image) ([]byte, error) {
	if img == nil {
		return nil, fmt.Errorf("missing image")
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeAssetPNGBytes(assetsDir, fileName string, data []byte) error {
	if assetsDir == "" || len(data) == 0 {
		return fmt.Errorf("missing assets dir")
	}
	dst := filepath.Join(assetsDir, fileName)
	tmp, err := os.CreateTemp(assetsDir, ".tmp-*.png")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func writeAssetPNG(assetsDir, fileName string, img image.Image) error {
	data, err := encodeAssetPNG(img)
	if err != nil {
		return err
	}
	return writeAssetPNGBytes(assetsDir, fileName, data)
}

func normalizePDFName(raw, fallback string) string {
	outName := strings.TrimSpace(raw)
	if outName == "" {
		outName = fallback
	}
	if !strings.HasSuffix(strings.ToLower(outName), ".pdf") {
		outName += ".pdf"
	}
	return outName
}

func splitPickedPaths(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parsePageRanges(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseIncludedPages(raw string, maxPage int) map[int]bool {
	included := make(map[int]bool)
	if maxPage <= 0 {
		return included
	}
	for _, part := range parsePageRanges(raw) {
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			start, e1 := strconv.Atoi(strings.TrimSpace(bounds[0]))
			end, e2 := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if e1 == nil && e2 == nil {
				if start > end {
					start, end = end, start
				}
				for p := start; p <= end; p++ {
					if p >= 1 && p <= maxPage {
						included[p] = true
					}
				}
			}
			continue
		}
		if p, err := strconv.Atoi(part); err == nil && p >= 1 && p <= maxPage {
			included[p] = true
		}
	}
	return included
}

func togglePageInRange(raw string, page int, maxPage int) string {
	included := parseIncludedPages(raw, maxPage)
	if included[page] {
		delete(included, page)
	} else if page >= 1 && page <= maxPage {
		included[page] = true
	}
	if len(included) == 0 {
		return ""
	}
	pages := make([]int, 0, len(included))
	for p := range included {
		pages = append(pages, p)
	}
	sort.Ints(pages)

	var parts []string
	start, prev := pages[0], pages[0]
	for i := 1; i < len(pages); i++ {
		if pages[i] == prev+1 {
			prev = pages[i]
			continue
		}
		parts = append(parts, formatPageRun(start, prev))
		start, prev = pages[i], pages[i]
	}
	parts = append(parts, formatPageRun(start, prev))
	return strings.Join(parts, ", ")
}

func formatPageRun(start, end int) string {
	if start == end {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func formatLiveFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

package pdf

import (
	"bufio"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pdfeng "github.com/seiortech/mtools-plugin-pdf/internal/pdf"
	"github.com/seiortech/mtools-sdk/pluginapi"
)

func createSamplePNG(t *testing.T, path string, col color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 80, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 80; x++ {
			img.Set(x, y, col)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func createSamplePDF(t *testing.T, dir, name string, pages int) string {
	t.Helper()
	var imgs []string
	for i := 0; i < pages; i++ {
		p := filepath.Join(dir, name+"_"+string(rune('a'+i))+".png")
		createSamplePNG(t, p, color.RGBA{R: 74, G: 93, B: 69, A: 255})
		imgs = append(imgs, p)
	}
	out := filepath.Join(dir, name+".pdf")
	if err := pdfeng.ImagesToPDF(context.Background(), imgs, out); err != nil {
		t.Fatalf("ImagesToPDF %s: %v", name, err)
	}
	return out
}

func TestLiveUIOpenTabsAndValidation(t *testing.T) {
	ui := NewLiveUI()
	tree, err := ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1180, Height: 760},
		Lang:     "en",
		Settings: map[string]string{"output_dir": t.TempDir()},
	}, nil)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("initial tree invalid: %v", err)
	}

	for _, tab := range []string{"split", "images", "office", "merge"} {
		if err := ui.Event(context.Background(), pluginapi.UIEvent{
			Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: tab,
		}, nil); err != nil {
			t.Fatalf("tab %s event failed: %v", tab, err)
		}
		ui.mu.Lock()
		next := ui.buildTreeLocked()
		ui.mu.Unlock()
		if err := next.Validate(); err != nil {
			t.Fatalf("tab %s tree invalid: %v", tab, err)
		}
	}
}

func TestLiveUIMergePreviewReorderAndJump(t *testing.T) {
	dir := t.TempDir()
	pdfA := createSamplePDF(t, dir, "a", 2)
	pdfB := createSamplePDF(t, dir, "b", 3)

	ui := NewLiveUI()
	_, err := ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1180, Height: 760},
		Lang:     "en",
		Settings: map[string]string{"output_dir": dir},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := ui.Event(context.Background(), pluginapi.UIEvent{
		Rev: 2, Node: "pdf.merge.pick", Kind: pluginapi.UIEventChange, Value: pdfA + "\n" + pdfB,
	}, nil); err != nil {
		t.Fatalf("pick failed: %v", err)
	}

	ui.mu.Lock()
	if len(ui.mergeFiles) != 2 {
		t.Fatalf("expected 2 merge files, got %d", len(ui.mergeFiles))
	}
	if len(ui.previewPages) != 5 {
		t.Fatalf("expected 5 concatenated preview pages, got %d", len(ui.previewPages))
	}
	if ui.previewPages[0].SourceName != "a.pdf" || ui.previewPages[2].SourceName != "b.pdf" {
		t.Fatalf("unexpected preview order: %+v", ui.previewPages)
	}
	ui.mu.Unlock()

	// Move b.pdf up to first position
	if err := ui.Event(context.Background(), pluginapi.UIEvent{
		Rev: 3, Node: "pdf.merge.item.1.up", Kind: pluginapi.UIEventClick,
	}, nil); err != nil {
		t.Fatalf("move up failed: %v", err)
	}

	ui.mu.Lock()
	if ui.mergeFiles[0].Name != "b.pdf" || ui.previewPages[0].SourceName != "b.pdf" {
		t.Fatalf("expected b.pdf first after move up, got %s", ui.mergeFiles[0].Name)
	}
	ui.mu.Unlock()

	// Click second file in list -> jumps currentPage to page 4
	if err := ui.Event(context.Background(), pluginapi.UIEvent{
		Rev: 4, Node: "pdf.merge.item.1.jump", Kind: pluginapi.UIEventClick,
	}, nil); err != nil {
		t.Fatalf("jump failed: %v", err)
	}
	ui.mu.Lock()
	if ui.currentPage != 4 {
		t.Fatalf("expected currentPage=4 after jump to a.pdf, got %d", ui.currentPage)
	}
	finalTree := ui.buildTreeLocked()
	ui.mu.Unlock()

	raw, err := json.Marshal(finalTree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pluginapi.DecodeTree(raw); err != nil {
		t.Fatalf("DecodeTree failed: %v", err)
	}
}

func TestLiveUISplitInteractiveRangeAndZoom(t *testing.T) {
	dir := t.TempDir()
	pdfDoc := createSamplePDF(t, dir, "doc", 4)

	ui := NewLiveUI()
	_, _ = ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1180, Height: 760},
		Settings: map[string]string{"output_dir": dir},
	}, nil)

	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: "split"}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 2, Node: "pdf.split.pick", Kind: pluginapi.UIEventChange, Value: pdfDoc}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 3, Node: "pdf.split.mode", Kind: pluginapi.UIEventSelect, Value: "range"}, nil)

	// Click pages 2 and 1 in preview to toggle range
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 4, Node: "page_2", Kind: pluginapi.UIEventClick}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 5, Node: "page_1", Kind: pluginapi.UIEventClick}, nil)

	ui.mu.Lock()
	if ui.splitPageRange != "1-2" {
		t.Fatalf("expected splitPageRange=1-2, got %q", ui.splitPageRange)
	}
	prevZoom := ui.zoom
	ui.mu.Unlock()

	// Zoom in & collapse panel
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 6, Node: "pdf.zoom_in", Kind: pluginapi.UIEventClick}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 7, Node: "pdf.rail_toggle", Kind: pluginapi.UIEventClick}, nil)

	ui.mu.Lock()
	if ui.zoom <= prevZoom {
		t.Fatalf("expected zoom to increase from %v, got %v", prevZoom, ui.zoom)
	}
	if !ui.railCollapsed {
		t.Fatal("expected railCollapsed=true")
	}
	tree := ui.buildTreeLocked()
	ui.mu.Unlock()

	if err := tree.Validate(); err != nil {
		t.Fatalf("split tree invalid: %v", err)
	}
}

func TestLiveUIImagesMonotonicAssetNames(t *testing.T) {
	dir := t.TempDir()
	img1 := filepath.Join(dir, "1.png")
	img2 := filepath.Join(dir, "2.png")
	img3 := filepath.Join(dir, "3.png")
	createSamplePNG(t, img1, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	createSamplePNG(t, img2, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	createSamplePNG(t, img3, color.RGBA{R: 70, G: 80, B: 90, A: 255})

	ui := NewLiveUI()
	_, _ = ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1180, Height: 760},
		Settings: map[string]string{"output_dir": dir},
	}, nil)

	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: "images"}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 2, Node: "pdf.images.pick", Kind: pluginapi.UIEventChange, Value: img1 + "\n" + img2}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 3, Node: "pdf.images.item.0.rm", Kind: pluginapi.UIEventClick}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 4, Node: "pdf.images.pick", Kind: pluginapi.UIEventChange, Value: img3}, nil)

	ui.mu.Lock()
	defer ui.mu.Unlock()
	if len(ui.imageFiles) != 2 || len(ui.previewPages) != 2 {
		t.Fatalf("expected 2 images & 2 preview pages, got %d / %d", len(ui.imageFiles), len(ui.previewPages))
	}
	if !strings.HasSuffix(ui.imageFiles[1].Name, "3.png") {
		t.Fatalf("unexpected second image: %+v", ui.imageFiles[1])
	}
}

func findUINodeByID(node pluginapi.UINode, id string) (pluginapi.UINode, bool) {
	if node.ID == id {
		return node, true
	}
	for _, ch := range node.Children {
		if found, ok := findUINodeByID(ch, id); ok {
			return found, true
		}
	}
	return pluginapi.UINode{}, false
}

func TestLiveUIImmediateSaveOnClick(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "out")
	pdf1 := createSamplePDF(t, dir, "first", 1)
	pdf2 := createSamplePDF(t, dir, "second", 2)

	ui := NewLiveUI()
	tree, err := ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1180, Height: 760},
		Settings: map[string]string{"output_dir": outDir},
	}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Verify primary run button never attaches PickFile across tabs.
	for _, tab := range []string{"merge", "split", "images", "office"} {
		_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: tab}, nil)
		if tab == "split" {
			_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 2, Node: "pdf.split.mode", Kind: pluginapi.UIEventSelect, Value: "range"}, nil)
		}
		ui.mu.Lock()
		tr := ui.buildTreeLocked()
		ui.mu.Unlock()
		btn, ok := findUINodeByID(tr.Root, "pdf.run")
		if !ok {
			t.Fatalf("missing pdf.run button on tab %s", tab)
		}
		props, err := pluginapi.DecodeProps[pluginapi.UIButtonProps](btn)
		if err != nil {
			t.Fatalf("decode pdf.run on tab %s: %v", tab, err)
		}
		if props.PickFile != nil {
			t.Fatalf("expected pdf.run PickFile to be nil on tab %s, got %+v", tab, props.PickFile)
		}
	}

	// Switch back to merge, stage two PDFs, set output filename, and click pdf.run.
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 3, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: "merge"}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 4, Node: "pdf.merge.pick", Kind: pluginapi.UIEventChange, Value: pdf1 + "\n" + pdf2}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 5, Node: "pdf.merge.outname", Kind: pluginapi.UIEventChange, Value: "combined.pdf"}, nil)
	if err := ui.Event(context.Background(), pluginapi.UIEvent{Rev: 6, Node: "pdf.run", Kind: pluginapi.UIEventClick}, nil); err != nil {
		t.Fatalf("click pdf.run: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ui.mu.Lock()
		running := ui.running
		msg := ui.resultMsg
		errMsg := ui.inlineError
		ui.mu.Unlock()
		if !running && (msg != "" || errMsg != "") {
			if errMsg != "" {
				t.Fatalf("unexpected merge error: %s", errMsg)
			}
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	wantOut := filepath.Join(outDir, "combined.pdf")
	if _, err := os.Stat(wantOut); err != nil {
		t.Fatalf("expected merged output at %s: %v", wantOut, err)
	}
	_ = tree
}

func TestLiveUIEngineMissingBannerValidates(t *testing.T) {
	dir := t.TempDir()
	pdfDoc := createSamplePDF(t, dir, "doc", 2)

	ui := NewLiveUI()
	_, _ = ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1180, Height: 760},
		Settings: map[string]string{"output_dir": dir},
	}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: "split"}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 2, Node: "pdf.split.pick", Kind: pluginapi.UIEventChange, Value: pdfDoc}, nil)

	ui.mu.Lock()
	ui.previewEngineErr = pdfeng.ErrPDFiumMissing
	tree := ui.buildTreeLocked()
	ui.mu.Unlock()

	if err := tree.Validate(); err != nil {
		t.Fatalf("engine missing banner tree invalid: %v", err)
	}
	if _, ok := findNode(tree.Root, "download_pdfium"); !ok {
		t.Fatalf("expected download_pdfium button in engine banner")
	}
	if _, ok := findNode(tree.Root, "open_settings"); !ok {
		t.Fatalf("expected open_settings button in engine banner")
	}
}

func findNode(n pluginapi.UINode, id string) (pluginapi.UINode, bool) {
	if n.ID == id {
		return n, true
	}
	for _, ch := range n.Children {
		if found, ok := findNode(ch, id); ok {
			return found, true
		}
	}
	return pluginapi.UINode{}, false
}

func countNodes(n pluginapi.UINode) int {
	total := 1
	for _, ch := range n.Children {
		total += countNodes(ch)
	}
	return total
}

type fakeLargePDFRenderer struct {
	pages       int
	posterPage  int
	allCalls    atomic.Int32
	dimCalls    atomic.Int32
	renderCalls atomic.Int32
}

func (f *fakeLargePDFRenderer) PageCount(context.Context, string) (int, error) {
	return f.pages, nil
}

func (f *fakeLargePDFRenderer) PageDimensions(context.Context, string, int) (float64, float64, error) {
	f.dimCalls.Add(1)
	return 595, 842, nil
}

func (f *fakeLargePDFRenderer) AllPageDimensions(context.Context, string) ([]pdfeng.PageDim, error) {
	f.allCalls.Add(1)
	out := make([]pdfeng.PageDim, f.pages)
	for i := range out {
		if i+1 == f.posterPage {
			out[i] = pdfeng.PageDim{Width: 3000, Height: 4000}
		} else {
			out[i] = pdfeng.PageDim{Width: 595, Height: 842}
		}
	}
	return out, nil
}

func (f *fakeLargePDFRenderer) RenderPage(_ context.Context, _ string, pageNum int, _ float64) (image.Image, error) {
	f.renderCalls.Add(1)
	img := image.NewRGBA(image.Rect(0, 0, 40, 60))
	c := color.RGBA{R: uint8(pageNum % 255), G: 100, B: 80, A: 255}
	for y := 0; y < 60; y++ {
		for x := 0; x < 40; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img, nil
}

func TestLiveUILargeDocChunkedSpacersAndZoomOut(t *testing.T) {
	dir := t.TempDir()
	fakePDF := filepath.Join(dir, "huge.pdf")
	if err := os.WriteFile(fakePDF, []byte("%PDF-1.4"), 0600); err != nil {
		t.Fatal(err)
	}

	ui := NewLiveUI()
	defer func() { _ = ui.Close(context.Background()) }()
	fr := &fakeLargePDFRenderer{pages: 1200, posterPage: 600}
	ui.renderer = fr

	if _, err := ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1200, Height: 800},
		Settings: map[string]string{"output_dir": dir},
	}, nil); err != nil {
		t.Fatal(err)
	}

	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: "split"}, nil)
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 2, Node: "pdf.split.pick", Kind: pluginapi.UIEventChange, Value: fakePDF}, nil)

	if fr.allCalls.Load() != 1 || fr.dimCalls.Load() != 0 {
		t.Fatalf("expected single AllPageDimensions call, got all=%d dim=%d", fr.allCalls.Load(), fr.dimCalls.Load())
	}

	// Jump deep into 1200-page doc (poster page at 600) and zoom to 3.0x.
	ui.mu.Lock()
	ui.zoom = 3.0
	ui.jumpToPageLocked(600, nil)
	tree := ui.buildTreeLocked()
	ui.mu.Unlock()

	if err := tree.Validate(); err != nil {
		t.Fatalf("1200-page tree failed Validate() (MaxUINodes/8192/1e6): %v", err)
	}
	if n := countNodes(tree.Root); n > 600 {
		t.Fatalf("expected chunked spacers to keep node count well below MaxUINodes (2048), got %d nodes", n)
	}

	// Regression test: pinch/wheel zoom OUT (factor < 1) must reduce zoom, and factor > 1 must increase zoom.
	ui.mu.Lock()
	ui.zoom = 1.5
	ui.mu.Unlock()
	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 3, Node: "pdf.viewport", Kind: pluginapi.UIEventZoom, Value: "0.8"}, nil)
	ui.mu.Lock()
	afterOut := ui.zoom
	ui.mu.Unlock()
	if afterOut >= 1.5 {
		t.Fatalf("expected zoom out (0.8) to decrease zoom below 1.5, got %v", afterOut)
	}

	_ = ui.Event(context.Background(), pluginapi.UIEvent{Rev: 4, Node: "pdf.viewport", Kind: pluginapi.UIEventZoom, Value: "1.25"}, nil)
	ui.mu.Lock()
	afterIn := ui.zoom
	ui.mu.Unlock()
	if afterIn <= afterOut {
		t.Fatalf("expected zoom in (1.25) to increase zoom above %v, got %v", afterOut, afterIn)
	}
}

func TestLiveUISettingsReloadAndViewport(t *testing.T) {
	dataDir := t.TempDir()
	outDir1 := filepath.Join(dataDir, "out1")
	outDir2 := filepath.Join(dataDir, "out2")

	ui := NewLiveUI()
	defer func() { _ = ui.Close(context.Background()) }()
	if _, err := ui.Open(context.Background(), pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1000, Height: 700},
		Settings: map[string]string{
			"plugin_data_dir": dataDir,
			"output_dir":      outDir1,
		},
	}, nil); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(map[string]string{"output_dir": outDir2})
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ui.Event(context.Background(), pluginapi.UIEvent{Rev: 1, Node: "settings", Kind: pluginapi.UIEventChange}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ui.Event(context.Background(), pluginapi.UIEvent{Rev: 2, Node: "root", Kind: pluginapi.UIEventView, Value: "1440x920"}, nil); err != nil {
		t.Fatal(err)
	}

	ui.mu.Lock()
	gotOut := ui.outputDir
	gotW, gotH := ui.viewportW, ui.viewportH
	ui.mu.Unlock()
	if gotOut != outDir2 {
		t.Fatalf("expected reloaded outputDir=%q, got %q", outDir2, gotOut)
	}
	if gotW != 1440 || gotH != 920 {
		t.Fatalf("expected viewport 1440x920, got %.0fx%.0f", gotW, gotH)
	}
}

type liveHarness struct {
	t         *testing.T
	inWrite   *io.PipeWriter
	done      chan error
	mu        sync.Mutex
	trees     []pluginapi.UITree
	closed    bool
	drainDone chan struct{}
}

func startPDFHarness(t *testing.T, assetsDir string, live *LiveUI) *liveHarness {
	t.Helper()
	t.Setenv("MTOOLS_UI_ASSETS", assetsDir)
	inRead, inWrite := io.Pipe()
	outRead, outWrite := io.Pipe()
	id := pluginapi.InitializeResult{Protocol: pluginapi.Protocol, PluginID: "com.mtools.pdf", Version: "1.0.0"}

	done := make(chan error, 1)
	go func() {
		done <- pluginapi.ServeLive(context.Background(), inRead, outWrite, id, nil, live)
		_ = outWrite.Close()
	}()

	h := &liveHarness{
		t:         t,
		inWrite:   inWrite,
		done:      done,
		drainDone: make(chan struct{}),
	}

	send := func(m pluginapi.Message) {
		t.Helper()
		if err := pluginapi.WriteMessage(inWrite, m); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
	}
	reader := bufio.NewReader(outRead)
	recv := func() pluginapi.Message {
		t.Helper()
		m, err := pluginapi.ReadMessage(reader)
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		return m
	}

	initRaw, _ := json.Marshal(id)
	send(pluginapi.Message{JSONRPC: "2.0", ID: 1, Method: pluginapi.MethodInitialize, Params: initRaw})
	if r := recv(); r.Error != nil {
		t.Fatalf("init error: %+v", r.Error)
	}
	openRaw, _ := json.Marshal(pluginapi.UIOpenParams{
		Tool:     "pdf",
		Viewport: pluginapi.UIViewport{Width: 1200, Height: 800},
		Settings: map[string]string{"output_dir": t.TempDir()},
	})
	send(pluginapi.Message{JSONRPC: "2.0", ID: 2, Method: pluginapi.MethodUIOpen, Params: openRaw})
	r2 := recv()
	if r2.Error != nil {
		t.Fatalf("open error: %+v", r2.Error)
	}
	tr, err := pluginapi.DecodeTree(r2.Result)
	if err != nil {
		t.Fatalf("DecodeTree: %v", err)
	}
	h.trees = append(h.trees, tr)

	go func() {
		defer close(h.drainDone)
		for {
			m, err := pluginapi.ReadMessage(reader)
			if err != nil {
				return
			}
			if m.Method == pluginapi.MethodUITree {
				if tr, err := pluginapi.DecodeTree(m.Params); err == nil {
					h.mu.Lock()
					h.trees = append(h.trees, tr)
					h.mu.Unlock()
				}
			}
		}
	}()
	return h
}

func (h *liveHarness) sendEvent(ev pluginapi.UIEvent) {
	h.t.Helper()
	raw, _ := json.Marshal(ev)
	if err := pluginapi.WriteMessage(h.inWrite, pluginapi.Message{
		JSONRPC: "2.0", Method: pluginapi.MethodUIEvent, Params: raw,
	}); err != nil {
		h.t.Fatalf("sendEvent: %v", err)
	}
}

func (h *liveHarness) close() {
	if h.closed {
		return
	}
	h.closed = true
	raw, _ := json.Marshal(pluginapi.UICloseParams{Tool: "pdf"})
	_ = pluginapi.WriteMessage(h.inWrite, pluginapi.Message{
		JSONRPC: "2.0", Method: pluginapi.MethodUIClose, Params: raw,
	})
	_ = h.inWrite.Close()
	if err := <-h.done; err != nil {
		h.t.Fatalf("ServeLive exit error: %v", err)
	}
	<-h.drainDone
}

func TestLiveUIAssetLRUEvictionAndClose(t *testing.T) {
	assetsDir := t.TempDir()
	fakePDF := filepath.Join(t.TempDir(), "many.pdf")
	if err := os.WriteFile(fakePDF, []byte("%PDF-1.4"), 0600); err != nil {
		t.Fatal(err)
	}

	ui := NewLiveUI()
	ui.renderer = &fakeLargePDFRenderer{pages: 60}
	h := startPDFHarness(t, assetsDir, ui)

	h.sendEvent(pluginapi.UIEvent{Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: "split"})
	h.sendEvent(pluginapi.UIEvent{Rev: 2, Node: "pdf.split.pick", Kind: pluginapi.UIEventChange, Value: fakePDF})

	// Step through 37 pages in increments of 3 so >25 distinct pages are rendered to assetsDir.
	for p := 1; p <= 37; p += 3 {
		h.sendEvent(pluginapi.UIEvent{Rev: 3, Node: "rail_page_" + strconv.Itoa(p), Kind: pluginapi.UIEventClick})
		time.Sleep(25 * time.Millisecond)
	}

	// Wait for in-flight renders to settle.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ui.mu.Lock()
		inflight := len(ui.inFlightRenders)
		ui.mu.Unlock()
		if inflight == 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Fatal(err)
	}
	pageFiles := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "page-") && strings.HasSuffix(e.Name(), ".png") {
			pageFiles++
		}
	}
	if pageFiles == 0 || pageFiles > maxOnDiskPageAssets {
		t.Fatalf("expected 1..%d on-disk page assets after LRU eviction, got %d", maxOnDiskPageAssets, pageFiles)
	}

	h.close()
	if err := ui.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	entriesAfter, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entriesAfter {
		if strings.HasPrefix(e.Name(), "page-") {
			t.Fatalf("page asset leaked after Close: %s", e.Name())
		}
	}
}

func TestLiveUISplitValidationAndLargeImagePreview(t *testing.T) {
	assetsDir := t.TempDir()
	dir := t.TempDir()
	pdfDoc := createSamplePDF(t, dir, "four", 4)

	// Create a large 1800x1200 PNG to trigger bounded preview downscaling (<=1400px).
	bigImgPath := filepath.Join(dir, "big.png")
	bigImg := image.NewRGBA(image.Rect(0, 0, 1800, 1200))
	f, err := os.Create(bigImgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, bigImg); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	ui := NewLiveUI()
	defer func() { _ = ui.Close(context.Background()) }()
	h := startPDFHarness(t, assetsDir, ui)
	defer h.close()

	// Split inline validation tests.
	h.sendEvent(pluginapi.UIEvent{Rev: 1, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: "split"})
	h.sendEvent(pluginapi.UIEvent{Rev: 2, Node: "pdf.split.pick", Kind: pluginapi.UIEventChange, Value: pdfDoc})
	h.sendEvent(pluginapi.UIEvent{Rev: 3, Node: "pdf.split.span", Kind: pluginapi.UIEventChange, Value: "0"})

	deadline := time.Now().Add(2 * time.Second)
	var spanErr string
	var canRunSpan bool
	for time.Now().Before(deadline) {
		ui.mu.Lock()
		spanErr = ui.splitSpanErr
		canRunSpan = ui.canRunLocked()
		ui.mu.Unlock()
		if spanErr != "" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if spanErr == "" || canRunSpan {
		t.Fatalf("expected invalid split span error and disabled run, got err=%q canRun=%v", spanErr, canRunSpan)
	}

	h.sendEvent(pluginapi.UIEvent{Rev: 4, Node: "pdf.split.mode", Kind: pluginapi.UIEventSelect, Value: "range"})
	h.sendEvent(pluginapi.UIEvent{Rev: 5, Node: "pdf.split.range", Kind: pluginapi.UIEventChange, Value: "1-99"})

	deadline = time.Now().Add(2 * time.Second)
	var rangeErr string
	var canRunRange bool
	var tr pluginapi.UITree
	for time.Now().Before(deadline) {
		ui.mu.Lock()
		rangeErr = ui.splitRangeErr
		canRunRange = ui.canRunLocked()
		if rangeErr != "" {
			tr = ui.buildTreeLocked()
		}
		ui.mu.Unlock()
		if rangeErr != "" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if rangeErr == "" || canRunRange {
		t.Fatalf("expected out-of-range error and disabled run, got err=%q canRun=%v", rangeErr, canRunRange)
	}
	if _, ok := findNode(tr.Root, "pdf.split.range_err"); !ok {
		t.Fatal("expected inline pdf.split.range_err node")
	}

	// Large image preview downscaling test.
	h.sendEvent(pluginapi.UIEvent{Rev: 6, Node: "pdf.tabs", Kind: pluginapi.UIEventSelect, Value: "images"})
	h.sendEvent(pluginapi.UIEvent{Rev: 7, Node: "pdf.images.pick", Kind: pluginapi.UIEventChange, Value: bigImgPath})
	deadline = time.Now().Add(2 * time.Second)
	var assetFile string
	for time.Now().Before(deadline) {
		ui.mu.Lock()
		if len(ui.imageFiles) == 1 {
			assetFile = ui.imageFiles[0].AssetFile
		}
		ui.mu.Unlock()
		if assetFile != "" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if assetFile == "" {
		t.Fatal("expected downscaled preview asset for 1800x1200 image")
	}
	af, err := os.Open(filepath.Join(assetsDir, assetFile))
	if err != nil {
		t.Fatalf("open preview asset: %v", err)
	}
	cfg, _, err := image.DecodeConfig(af)
	_ = af.Close()
	if err != nil {
		t.Fatalf("decode preview asset: %v", err)
	}
	if cfg.Width > maxImagePreviewDim || cfg.Height > maxImagePreviewDim {
		t.Fatalf("expected preview downscaled <=%dpx, got %dx%d", maxImagePreviewDim, cfg.Width, cfg.Height)
	}
}

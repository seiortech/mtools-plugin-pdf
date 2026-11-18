package pdf

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
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

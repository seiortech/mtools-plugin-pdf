package pdf

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func findTestPDFiumWASM(t *testing.T) string {
	t.Helper()
	if p := strings.TrimSpace(os.Getenv("MTOOLS_TEST_PDFIUM_WASM")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if out, err := exec.Command("go", "env", "GOMODCACHE").Output(); err == nil {
		modCache := strings.TrimSpace(string(out))
		if modCache != "" {
			candidate := filepath.Join(modCache, "github.com", "klippa-app", "go-pdfium@v1.21.4", "internal", "pdfium_wasm", "pdfium.wasm")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func configureTestRenderer(t *testing.T, r PageRenderer) {
	t.Helper()
	cr, ok := r.(ConfigurableRenderer)
	if !ok {
		return
	}
	wasmPath := findTestPDFiumWASM(t)
	if wasmPath == "" {
		t.Skip("pdfium.wasm not found in MTOOLS_TEST_PDFIUM_WASM or GOMODCACHE")
	}
	cacheDir := filepath.Join(os.TempDir(), "mtools-test-pdfium-cache")
	cr.Configure(RendererConfig{
		WASMPath: wasmPath,
		CacheDir: cacheDir,
	})
	t.Cleanup(func() {
		_ = cr.Close()
	})
}

func TestQuantizeRenderScale(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 1.0},
		{0.1, 0.25},
		{0.62, 0.5},
		{0.63, 0.75},
		{1.0, 1.0},
		{2.38, 2.5},
		{4.5, 3.0},
	}
	for _, tc := range cases {
		if got := QuantizeRenderScale(tc.in); got != tc.want {
			t.Fatalf("QuantizeRenderScale(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPageBitmapCache(t *testing.T) {
	c := NewPageBitmapCache(2)
	img1 := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img2 := image.NewRGBA(image.Rect(0, 0, 20, 20))
	img3 := image.NewRGBA(image.Rect(0, 0, 30, 30))

	c.Put("/a.pdf", 1, 1.0, img1)
	c.Put("/a.pdf", 2, 1.0, img2)
	if _, ok := c.Get("/a.pdf", 1, 1.0); !ok {
		t.Fatal("expected page 1 hit")
	}
	c.Put("/b.pdf", 1, 1.0, img3)
	if _, ok := c.Get("/a.pdf", 2, 1.0); ok {
		t.Fatal("expected page 2 to be evicted as LRU")
	}
	c.ClearForPath("/a.pdf")
	if _, ok := c.Get("/a.pdf", 1, 1.0); ok {
		t.Fatal("expected /a.pdf cleared")
	}
	if _, ok := c.Get("/b.pdf", 1, 1.0); !ok {
		t.Fatal("expected /b.pdf retained")
	}
}

func TestFallbackAndDefaultRenderer(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "sample.png")
	createTestPNG(t, imgPath, color.RGBA{R: 74, G: 93, B: 69, A: 255})

	pdfPath := filepath.Join(dir, "sample.pdf")
	if err := ImagesToPDF(context.Background(), []string{imgPath}, pdfPath); err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}

	fb := &FallbackRenderer{}
	cnt, err := fb.PageCount(context.Background(), pdfPath)
	if err != nil || cnt != 1 {
		t.Fatalf("FallbackRenderer.PageCount = (%d, %v), want (1, nil)", cnt, err)
	}
	w, h, err := fb.PageDimensions(context.Background(), pdfPath, 1)
	if err != nil || w <= 0 || h <= 0 {
		t.Fatalf("FallbackRenderer.PageDimensions = (%v, %v, %v)", w, h, err)
	}
	_, fbErr := fb.RenderPage(context.Background(), pdfPath, 1, 1.0)
	if _, lookErr := exec.LookPath("pdftoppm"); lookErr == nil {
		if fbErr != nil {
			t.Fatalf("FallbackRenderer.RenderPage with pdftoppm: %v", fbErr)
		}
	} else if !errors.Is(fbErr, ErrNoPreviewEngine) {
		t.Fatalf("FallbackRenderer.RenderPage = %v, want ErrNoPreviewEngine", fbErr)
	}

	dr := DefaultRenderer()
	configureTestRenderer(t, dr)
	img, err := dr.RenderPage(context.Background(), pdfPath, 1, 1.0)
	if err != nil || img.Bounds().Dx() <= 0 || img.Bounds().Dy() <= 0 {
		t.Fatalf("DefaultRenderer.RenderPage = (%v, %v)", img, err)
	}
}

type mockBatchRenderer struct {
	dims       []PageDim
	allCalls   int
	countCalls int
	dimCalls   int
}

func (m *mockBatchRenderer) PageCount(context.Context, string) (int, error) {
	m.countCalls++
	return len(m.dims), nil
}

func (m *mockBatchRenderer) PageDimensions(context.Context, string, int) (float64, float64, error) {
	m.dimCalls++
	return 595, 842, nil
}

func (m *mockBatchRenderer) RenderPage(context.Context, string, int, float64) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 10, 10)), nil
}

func (m *mockBatchRenderer) AllPageDimensions(context.Context, string) ([]PageDim, error) {
	m.allCalls++
	return m.dims, nil
}

func TestAllPageDimensionsSinglePass(t *testing.T) {
	mock := &mockBatchRenderer{
		dims: []PageDim{{Width: 200, Height: 300}, {Width: 400, Height: 500}},
	}
	got, err := AllPageDimensions(context.Background(), mock, "/fake.pdf")
	if err != nil {
		t.Fatalf("AllPageDimensions mock: %v", err)
	}
	if len(got) != 2 || got[0].Width != 200 || got[1].Height != 500 {
		t.Fatalf("unexpected dims: %+v", got)
	}
	if mock.allCalls != 1 || mock.countCalls != 0 || mock.dimCalls != 0 {
		t.Fatalf("expected single batch call, got all=%d count=%d dim=%d", mock.allCalls, mock.countCalls, mock.dimCalls)
	}

	dir := t.TempDir()
	img1 := filepath.Join(dir, "1.png")
	img2 := filepath.Join(dir, "2.png")
	createTestPNG(t, img1, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	createTestPNG(t, img2, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	pdfPath := filepath.Join(dir, "two.pdf")
	if err := ImagesToPDF(context.Background(), []string{img1, img2}, pdfPath); err != nil {
		t.Fatal(err)
	}

	fbDims, err := AllPageDimensions(context.Background(), &FallbackRenderer{}, pdfPath)
	if err != nil || len(fbDims) != 2 || fbDims[0].Width <= 0 || fbDims[1].Height <= 0 {
		t.Fatalf("FallbackRenderer AllPageDimensions = (%+v, %v)", fbDims, err)
	}

	dr := DefaultRenderer()
	configureTestRenderer(t, dr)
	drDims, err := AllPageDimensions(context.Background(), dr, pdfPath)
	if err != nil || len(drDims) != 2 || drDims[0].Width <= 0 || drDims[1].Height <= 0 {
		t.Fatalf("DefaultRenderer AllPageDimensions = (%+v, %v)", drDims, err)
	}
}

func TestPageBitmapCacheAndPageBytesCacheByteBudget(t *testing.T) {
	// 100x100 RGBA = 40,000 bytes; budget 80,000 allows 2 entries.
	bm := NewPageBitmapCacheWithBudget(10, 80_000)
	bm.Put("/a.pdf", 1, 1.0, image.NewRGBA(image.Rect(0, 0, 100, 100)))
	bm.Put("/a.pdf", 2, 1.0, image.NewRGBA(image.Rect(0, 0, 100, 100)))
	bm.Put("/a.pdf", 3, 1.0, image.NewRGBA(image.Rect(0, 0, 100, 100)))
	if bm.Bytes() > 80_000 {
		t.Fatalf("bitmap cache exceeded byte budget: %d", bm.Bytes())
	}
	if _, ok := bm.Get("/a.pdf", 1, 1.0); ok {
		t.Fatal("expected oldest bitmap entry to be evicted by byte budget")
	}

	bc := NewPageBytesCache(1000)
	bc.Put("/a.pdf", 1, 1.0, make([]byte, 400))
	bc.Put("/a.pdf", 2, 1.0, make([]byte, 400))
	bc.Put("/b.pdf", 1, 1.0, make([]byte, 400))
	if bc.Bytes() > 1000 {
		t.Fatalf("bytes cache exceeded budget: %d", bc.Bytes())
	}
	if _, ok := bc.Get("/a.pdf", 1, 1.0); ok {
		t.Fatal("expected page 1 evicted by byte budget")
	}
	if _, ok := bc.Get("/a.pdf", 2, 1.0); !ok {
		t.Fatal("expected page 2 retained")
	}
	bc.Put("/big.pdf", 1, 1.0, make([]byte, 2000)) // exceeds maxBytes -> ignored
	if _, ok := bc.Get("/big.pdf", 1, 1.0); ok {
		t.Fatal("oversized entry should not be cached")
	}
	bc.ClearForPath("/a.pdf")
	if bc.Bytes() != 400 {
		t.Fatalf("expected 400 bytes remaining after ClearForPath, got %d", bc.Bytes())
	}
}

func TestAdaptiveRenderScaleCapsLargePosters(t *testing.T) {
	if got := computeAdaptiveRenderScale(1.0, 595, 842, defaultRenderMaxDimPx, defaultRenderMaxPixels); got != 2.0 {
		t.Fatalf("normal A4 @ 1.0x = %v, want 2.0", got)
	}
	if got := computeAdaptiveRenderScale(3.0, 595, 842, defaultRenderMaxDimPx, defaultRenderMaxPixels); got != 3.0 {
		t.Fatalf("normal A4 @ 3.0x = %v, want 3.0 (not 6.0)", got)
	}
	// A0 poster: 2384 x 3370 pt at 3.0x zoom must stay <= 4096px and <= 16 MP.
	posterScale := computeAdaptiveRenderScale(3.0, 2384, 3370, defaultRenderMaxDimPx, defaultRenderMaxPixels)
	pw := 2384 * posterScale
	ph := 3370 * posterScale
	if pw > defaultRenderMaxDimPx+1 || ph > defaultRenderMaxDimPx+1 {
		t.Fatalf("poster dims exceed 4096px: %.1fx%.1f (scale=%v)", pw, ph, posterScale)
	}
	if pw*ph > float64(defaultRenderMaxPixels)+1024 {
		t.Fatalf("poster pixels exceed 16MP: %.0f (scale=%v)", pw*ph, posterScale)
	}
}

func TestEmptyPDFDoesNotPanic(t *testing.T) {
	emptyPath := filepath.Join(t.TempDir(), "empty.pdf")
	if err := os.WriteFile(emptyPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	dr := DefaultRenderer()
	configureTestRenderer(t, dr)
	if _, err := dr.PageCount(context.Background(), emptyPath); err == nil {
		t.Fatal("expected error for 0-byte PDF PageCount")
	}
	if _, err := AllPageDimensions(context.Background(), dr, emptyPath); err == nil {
		t.Fatal("expected error for 0-byte PDF AllPageDimensions")
	}
	if _, err := dr.RenderPage(context.Background(), emptyPath, 1, 1.0); err == nil {
		t.Fatal("expected error for 0-byte PDF RenderPage")
	}
}

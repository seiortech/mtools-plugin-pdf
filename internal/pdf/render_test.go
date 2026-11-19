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

package pdf

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func createTestPNG(t *testing.T, path string, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, c)
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

// Recovered from e605af4: exercise actual PDFs, not mocked engine results.
func TestPDFOperations(t *testing.T) {
	dir, ctx := t.TempDir(), t.Context()
	img1, img2 := filepath.Join(dir, "red.png"), filepath.Join(dir, "green.png")
	createTestPNG(t, img1, color.RGBA{R: 255, A: 255})
	createTestPNG(t, img2, color.RGBA{G: 255, A: 255})
	pdf1, pdf2 := filepath.Join(dir, "doc1.pdf"), filepath.Join(dir, "doc2.pdf")
	for i, in := range []string{img1, img2} {
		if err := ImagesToPDF(ctx, []string{in}, []string{pdf1, pdf2}[i]); err != nil {
			t.Fatal(err)
		}
	}
	assertPages := func(path string, want int) {
		t.Helper()
		got, err := GetPageCount(ctx, path)
		if err != nil || got != want {
			t.Fatalf("%s: pages=%d, want %d; error=%v", path, got, want, err)
		}
	}
	assertPages(pdf1, 1)
	assertPages(pdf2, 1)
	merged := filepath.Join(dir, "merged.pdf")
	if err := MergePDFs(ctx, []string{pdf1, pdf2}, merged); err != nil {
		t.Fatal(err)
	}
	assertPages(merged, 2)
	outputs, err := SplitPDF(ctx, merged, filepath.Join(dir, "split"), 1)
	if err != nil || len(outputs) != 2 {
		t.Fatalf("split: %v, %v", outputs, err)
	}
	for _, out := range outputs {
		assertPages(out, 1)
	}
	extracted := filepath.Join(dir, "extracted.pdf")
	if err := ExtractPages(ctx, merged, extracted, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	assertPages(extracted, 1)
	// Re-exporting must replace, not append to the previous PDF.
	if err := ImagesToPDF(ctx, []string{img1, img2}, pdf1); err != nil {
		t.Fatal(err)
	}
	assertPages(pdf1, 2)
	if err := ImagesToPDF(ctx, []string{img1}, pdf1); err != nil {
		t.Fatal(err)
	}
	assertPages(pdf1, 1)
}

func TestPDFCancellationAndOutputSafety(t *testing.T) {
	for _, op := range []string{"merge", "extract", "images"} {
		for _, canceled := range []bool{false, true} {
			t.Run(op+map[bool]string{false: "/failure", true: "/cancel"}[canceled], func(t *testing.T) {
				dir := t.TempDir()
				out := filepath.Join(dir, "existing.pdf")
				const original = "previous output must survive"
				if err := os.WriteFile(out, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if canceled {
					cancel()
				}
				missing := filepath.Join(dir, "missing")
				var err error
				switch op {
				case "merge":
					err = MergePDFs(ctx, []string{missing}, out)
				case "extract":
					err = ExtractPages(ctx, missing, out, []string{"1"})
				case "images":
					err = ImagesToPDF(ctx, []string{missing + ".png"}, out)
				}
				if err == nil || (canceled && !errors.Is(err, context.Canceled)) {
					t.Fatalf("expected failure/cancellation, got %v", err)
				}
				got, readErr := os.ReadFile(out)
				if readErr != nil || string(got) != original {
					t.Fatalf("previous output changed: %q %v", got, readErr)
				}
				files, readErr := os.ReadDir(dir)
				if readErr != nil || len(files) != 1 {
					t.Fatalf("temporary files leaked: %v %v", files, readErr)
				}
			})
		}
	}
}

func TestEmptyInputGuards(t *testing.T) {
	ctx := t.Context()
	if err := MergePDFs(ctx, nil, ""); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("MergePDFs empty: want ErrEmptyInput, got %v", err)
	}
	if _, err := SplitPDF(ctx, "", "", 1); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("SplitPDF empty: want ErrEmptyInput, got %v", err)
	}
	if err := ExtractPages(ctx, "", "", []string{"1"}); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("ExtractPages empty inFile: want ErrEmptyInput, got %v", err)
	}
	if err := ExtractPages(ctx, "a.pdf", "", nil); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("ExtractPages empty pages: want ErrInvalidRange, got %v", err)
	}
	if err := ImagesToPDF(ctx, nil, ""); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("ImagesToPDF empty: want ErrEmptyInput, got %v", err)
	}
	if _, err := GetPageCount(ctx, ""); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("GetPageCount empty: want ErrEmptyInput, got %v", err)
	}
}

func TestFindSofficePath(t *testing.T) {
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()

	lookPath = func(file string) (string, error) {
		if file == "soffice" {
			return "/custom/bin/soffice", nil
		}
		return "", errors.New("not found")
	}

	p, err := FindSofficePath("")
	if err != nil {
		t.Fatalf("expected to find soffice via lookPath, got err: %v", err)
	}
	if p != "/custom/bin/soffice" {
		t.Fatalf("expected /custom/bin/soffice, got %s", p)
	}

	// When lookPath fails and custom is empty
	lookPath = func(file string) (string, error) {
		return "", errors.New("not found")
	}
	_, err = FindSofficePath("/non/existent/soffice")
	if !errors.Is(err, ErrMissingSoffice) {
		t.Fatalf("expected ErrMissingSoffice, got: %v", err)
	}
}

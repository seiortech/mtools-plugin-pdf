//go:build darwin

package pdf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppleIWorkArgvAndErrorClassification(t *testing.T) {
	orig := runOsascript
	defer func() { runOsascript = orig }()

	t.Run("safe argv passing with spaces and quotes", func(t *testing.T) {
		var gotArgv []string
		runOsascript = func(ctx context.Context, argv ...string) ([]byte, error) {
			gotArgv = append([]string(nil), argv...)
			return nil, nil
		}

		inPath := `/tmp/my "report" $(whoami).docx`
		outPath := `/tmp/out "pdf".pdf`
		if err := convertWithAppleIWork(context.Background(), inPath, outPath); err != nil {
			t.Fatalf("convertWithAppleIWork failed: %v", err)
		}
		if len(gotArgv) != 5 || gotArgv[0] != "-e" || gotArgv[2] != "--" || gotArgv[3] != inPath || gotArgv[4] != outPath {
			t.Fatalf("unexpected osascript argv: %#v", gotArgv)
		}
		if gotArgv[1] != pagesExportScript {
			t.Fatalf("expected pagesExportScript for .docx")
		}
	})

	t.Run("classifies macOS TCC -1743 as ErrAutomationDenied", func(t *testing.T) {
		runOsascript = func(ctx context.Context, argv ...string) ([]byte, error) {
			return []byte(`execution error: Not authorized to send Apple events to Pages. (-1743)`), errors.New("exit status 1")
		}
		err := convertWithAppleIWork(context.Background(), "/tmp/test.pages", "/tmp/test.pdf")
		if !errors.Is(err, ErrAutomationDenied) {
			t.Fatalf("expected ErrAutomationDenied, got %v", err)
		}
	})

	t.Run("rejects unsupported OpenDocument format", func(t *testing.T) {
		err := convertWithAppleIWork(context.Background(), "/tmp/test.odt", "/tmp/test.pdf")
		if !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("expected ErrUnsupportedFormat, got %v", err)
		}
	})

	t.Run("numbers and keynote script selection", func(t *testing.T) {
		dir := t.TempDir()
		var lastScript string
		runOsascript = func(ctx context.Context, argv ...string) ([]byte, error) {
			lastScript = argv[1]
			return nil, os.WriteFile(argv[4], []byte("%PDF-1.4"), 0o644)
		}
		if err := convertWithAppleIWork(context.Background(), filepath.Join(dir, "sheet.xlsx"), filepath.Join(dir, "sheet.pdf")); err != nil {
			t.Fatal(err)
		}
		if lastScript != numbersExportScript {
			t.Fatalf("expected numbersExportScript for .xlsx")
		}
		if err := convertWithAppleIWork(context.Background(), filepath.Join(dir, "deck.pptx"), filepath.Join(dir, "deck.pdf")); err != nil {
			t.Fatal(err)
		}
		if lastScript != keynoteExportScript {
			t.Fatalf("expected keynoteExportScript for .pptx")
		}
	})
}

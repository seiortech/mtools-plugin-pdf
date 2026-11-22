package pdf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOfficeEngine(t *testing.T) {
	cases := []struct {
		in      string
		want    OfficeEngine
		wantErr error
	}{
		{"", OfficeEngineAuto, nil},
		{"auto", OfficeEngineAuto, nil},
		{"libreoffice", OfficeEngineLibreOffice, nil},
		{"msoffice", OfficeEngineMSOffice, nil},
		{"iwork", OfficeEngineAppleIWork, nil},
		{"wps", "", ErrUnsupportedEngine},
	}
	for _, tc := range cases {
		got, err := ParseOfficeEngine(tc.in)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("ParseOfficeEngine(%q) err = %v, want %v", tc.in, err, tc.wantErr)
		}
		if got != tc.want {
			t.Fatalf("ParseOfficeEngine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveOfficeBackendMatrix(t *testing.T) {
	winBoth := OfficeEngineStatus{
		LibreOfficePath: `C:\LibreOffice\soffice.exe`,
		MSOfficeApps:    []string{"Word", "Excel", "PowerPoint"},
	}
	winMSOnly := OfficeEngineStatus{
		MSOfficeApps: []string{"Word", "Excel"},
	}
	macBoth := OfficeEngineStatus{
		LibreOfficePath: "/Applications/LibreOffice.app/Contents/MacOS/soffice",
		IWorkApps:       []string{"Pages", "Numbers", "Keynote"},
	}
	macIWorkOnly := OfficeEngineStatus{
		IWorkApps: []string{"Pages", "Numbers", "Keynote"},
	}
	linuxLO := OfficeEngineStatus{
		LibreOfficePath: "/usr/bin/soffice",
	}

	tests := []struct {
		name    string
		engine  OfficeEngine
		ext     string
		status  OfficeEngineStatus
		goos    string
		want    OfficeEngine
		wantErr error
	}{
		{"win auto docx prefers msoffice", OfficeEngineAuto, ".docx", winBoth, "windows", OfficeEngineMSOffice, nil},
		{"win auto odt prefers libreoffice", OfficeEngineAuto, ".odt", winBoth, "windows", OfficeEngineLibreOffice, nil},
		{"win auto odt falls back to msoffice word", OfficeEngineAuto, ".odt", winMSOnly, "windows", OfficeEngineMSOffice, nil},
		{"win explicit iwork unsupported", OfficeEngineAppleIWork, ".docx", winBoth, "windows", "", ErrUnsupportedEngine},
		{"mac auto pages prefers iwork", OfficeEngineAuto, ".pages", macBoth, "darwin", OfficeEngineAppleIWork, nil},
		{"mac auto docx prefers libreoffice when installed", OfficeEngineAuto, ".docx", macBoth, "darwin", OfficeEngineLibreOffice, nil},
		{"mac auto docx falls back to iwork", OfficeEngineAuto, ".docx", macIWorkOnly, "darwin", OfficeEngineAppleIWork, nil},
		{"mac auto odt without libreoffice unsupported", OfficeEngineAuto, ".odt", macIWorkOnly, "darwin", "", ErrUnsupportedFormat},
		{"mac explicit msoffice unsupported", OfficeEngineMSOffice, ".docx", macBoth, "darwin", "", ErrUnsupportedEngine},
		{"mac explicit iwork odt unsupported", OfficeEngineAppleIWork, ".odt", macBoth, "darwin", "", ErrUnsupportedFormat},
		{"linux auto docx uses libreoffice", OfficeEngineAuto, ".docx", linuxLO, "linux", OfficeEngineLibreOffice, nil},
		{"empty engines returns ErrMissingOfficeEngine", OfficeEngineAuto, ".docx", OfficeEngineStatus{}, "linux", "", ErrMissingOfficeEngine},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveOfficeBackend(tc.engine, tc.ext, tc.status, tc.goos)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("backend = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOfficeStagedAtomicOutputSafety(t *testing.T) {
	dir := t.TempDir()
	inFile := filepath.Join(dir, "sample.docx")
	if err := os.WriteFile(inFile, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existingPDF := filepath.Join(outDir, "sample.pdf")
	if err := os.WriteFile(existingPDF, []byte("original-pdf"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ConvertOfficeToPDFWithSpec(ctx, OfficeConvertSpec{
		Engine:    OfficeEngineAuto,
		InputFile: inFile,
		OutputDir: outDir,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	got, err := os.ReadFile(existingPDF)
	if err != nil || string(got) != "original-pdf" {
		t.Fatalf("existing output PDF was modified on canceled run: %q err=%v", string(got), err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".mtools-office-") {
			t.Fatalf("leftover staging directory found: %s", e.Name())
		}
	}
}

package pdf

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OfficeEngine selects which local engine converts Office/iWork documents to PDF.
type OfficeEngine string

const (
	OfficeEngineAuto        OfficeEngine = "auto"
	OfficeEngineLibreOffice OfficeEngine = "libreoffice"
	OfficeEngineMSOffice    OfficeEngine = "msoffice"
	OfficeEngineAppleIWork  OfficeEngine = "iwork"
)

var (
	// ErrMissingOfficeEngine indicates no compatible local Office engine was found.
	ErrMissingOfficeEngine = errors.New("no compatible office conversion engine found")
	// ErrUnsupportedEngine indicates the requested engine is not available on this OS.
	ErrUnsupportedEngine = errors.New("office conversion engine is not supported on this operating system")
	// ErrUnsupportedFormat indicates the selected engine cannot convert the file extension.
	ErrUnsupportedFormat = errors.New("document format is not supported by the selected conversion engine")
	// ErrAutomationDenied indicates macOS Automation (TCC) permission was denied.
	ErrAutomationDenied = errors.New("macOS automation permission denied; enable mtools in System Settings -> Privacy & Security -> Automation")
)

// OfficeEngineStatus summarizes locally detected Office conversion engines.
type OfficeEngineStatus struct {
	LibreOfficePath string
	MSOfficeApps    []string // Windows: Word, Excel, PowerPoint
	IWorkApps       []string // macOS: Pages, Numbers, Keynote
}

// HasAny reports whether at least one conversion engine is detected.
func (s OfficeEngineStatus) HasAny() bool {
	return s.LibreOfficePath != "" || len(s.MSOfficeApps) > 0 || len(s.IWorkApps) > 0
}

// Summary returns a concise human-readable description of detected engines.
func (s OfficeEngineStatus) Summary() string {
	var parts []string
	if len(s.MSOfficeApps) > 0 {
		parts = append(parts, fmt.Sprintf("Microsoft Office (%s)", strings.Join(s.MSOfficeApps, ", ")))
	}
	if s.LibreOfficePath != "" {
		parts = append(parts, "LibreOffice")
	}
	if len(s.IWorkApps) > 0 {
		parts = append(parts, fmt.Sprintf("Apple iWork (%s)", strings.Join(s.IWorkApps, ", ")))
	}
	return strings.Join(parts, " + ")
}

// OfficeConvertSpec describes a single Office-to-PDF conversion request.
type OfficeConvertSpec struct {
	Engine      OfficeEngine
	SofficePath string
	InputFile   string
	OutputDir   string
}

// ParseOfficeEngine validates and normalizes a raw engine setting value.
func ParseOfficeEngine(raw string) (OfficeEngine, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(OfficeEngineAuto):
		return OfficeEngineAuto, nil
	case string(OfficeEngineLibreOffice):
		return OfficeEngineLibreOffice, nil
	case string(OfficeEngineMSOffice):
		return OfficeEngineMSOffice, nil
	case string(OfficeEngineAppleIWork):
		return OfficeEngineAppleIWork, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedEngine, raw)
	}
}

// DetectOfficeEngines probes locally available Office conversion engines without
// spawning external GUI processes.
func DetectOfficeEngines(customSofficePath string) OfficeEngineStatus {
	var st OfficeEngineStatus
	if p, err := FindSofficePath(customSofficePath); err == nil {
		st.LibreOfficePath = p
	}
	st.MSOfficeApps = detectMSOfficeApps()
	st.IWorkApps = detectIWorkApps()
	return st
}

// ResolveOfficeBackend selects the concrete conversion engine for ext on goos.
func ResolveOfficeBackend(engine OfficeEngine, ext string, status OfficeEngineStatus, goos string) (OfficeEngine, error) {
	ext = strings.ToLower(strings.TrimSpace(ext))
	switch engine {
	case "", OfficeEngineAuto:
		return resolveAutoOfficeBackend(ext, status, goos)

	case OfficeEngineLibreOffice:
		if status.LibreOfficePath == "" {
			return "", fmt.Errorf("%w: %w", ErrMissingOfficeEngine, ErrMissingSoffice)
		}
		return OfficeEngineLibreOffice, nil

	case OfficeEngineMSOffice:
		if goos != "windows" {
			return "", fmt.Errorf("%w: Microsoft Office COM is only available on Windows", ErrUnsupportedEngine)
		}
		app := msOfficeAppForExt(ext)
		if app == "" {
			return "", fmt.Errorf("%w: Microsoft Office cannot convert %s", ErrUnsupportedFormat, ext)
		}
		if !containsString(status.MSOfficeApps, app) {
			return "", fmt.Errorf("%w: Microsoft %s is not installed", ErrMissingOfficeEngine, app)
		}
		return OfficeEngineMSOffice, nil

	case OfficeEngineAppleIWork:
		if goos != "darwin" {
			return "", fmt.Errorf("%w: Apple iWork is only available on macOS", ErrUnsupportedEngine)
		}
		app := iWorkAppForExt(ext)
		if app == "" {
			return "", fmt.Errorf("%w: Apple iWork cannot convert %s (use LibreOffice for OpenDocument files)", ErrUnsupportedFormat, ext)
		}
		if !containsString(status.IWorkApps, app) {
			return "", fmt.Errorf("%w: Apple %s is not installed", ErrMissingOfficeEngine, app)
		}
		return OfficeEngineAppleIWork, nil

	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedEngine, engine)
	}
}

func resolveAutoOfficeBackend(ext string, status OfficeEngineStatus, goos string) (OfficeEngine, error) {
	switch goos {
	case "windows":
		// Prefer Microsoft Office for native MS formats; LibreOffice for ODF or fallback.
		if isNativeMSOfficeExt(ext) {
			if app := msOfficeAppForExt(ext); app != "" && containsString(status.MSOfficeApps, app) {
				return OfficeEngineMSOffice, nil
			}
		}
		if status.LibreOfficePath != "" {
			return OfficeEngineLibreOffice, nil
		}
		if app := msOfficeAppForExt(ext); app != "" && containsString(status.MSOfficeApps, app) {
			return OfficeEngineMSOffice, nil
		}
		return "", fmt.Errorf("%w: %w", ErrMissingOfficeEngine, ErrMissingSoffice)

	case "darwin":
		// Native iWork documents (.pages/.numbers/.key) prefer Apple iWork first.
		if isNativeIWorkExt(ext) {
			if app := iWorkAppForExt(ext); app != "" && containsString(status.IWorkApps, app) {
				return OfficeEngineAppleIWork, nil
			}
		}
		// Prefer headless LibreOffice when installed to avoid GUI focus/TCC prompts.
		if status.LibreOfficePath != "" {
			return OfficeEngineLibreOffice, nil
		}
		// Fallback to Apple iWork (Pages/Numbers/Keynote) for supported formats.
		if app := iWorkAppForExt(ext); app != "" {
			if containsString(status.IWorkApps, app) {
				return OfficeEngineAppleIWork, nil
			}
			return "", fmt.Errorf("%w: install LibreOffice or Apple %s to convert %s", ErrMissingOfficeEngine, app, ext)
		}
		if len(status.IWorkApps) > 0 {
			return "", fmt.Errorf("%w: %s requires LibreOffice (not supported by Apple iWork)", ErrUnsupportedFormat, ext)
		}
		return "", fmt.Errorf("%w: %w", ErrMissingOfficeEngine, ErrMissingSoffice)

	default:
		if status.LibreOfficePath != "" {
			return OfficeEngineLibreOffice, nil
		}
		return "", fmt.Errorf("%w: %w", ErrMissingOfficeEngine, ErrMissingSoffice)
	}
}

func isNativeMSOfficeExt(ext string) bool {
	switch ext {
	case ".doc", ".docx", ".rtf", ".xls", ".xlsx", ".ppt", ".pptx":
		return true
	default:
		return false
	}
}

func msOfficeAppForExt(ext string) string {
	switch ext {
	case ".doc", ".docx", ".rtf", ".odt":
		return "Word"
	case ".xls", ".xlsx", ".ods", ".csv":
		return "Excel"
	case ".ppt", ".pptx", ".odp":
		return "PowerPoint"
	default:
		return ""
	}
}

func isNativeIWorkExt(ext string) bool {
	switch ext {
	case ".pages", ".numbers", ".key":
		return true
	default:
		return false
	}
}

func iWorkAppForExt(ext string) string {
	switch ext {
	case ".pages", ".doc", ".docx", ".rtf":
		return "Pages"
	case ".numbers", ".xls", ".xlsx", ".csv":
		return "Numbers"
	case ".key", ".ppt", ".pptx":
		return "Keynote"
	default:
		return ""
	}
}

func containsString(items []string, target string) bool {
	for _, it := range items {
		if it == target {
			return true
		}
	}
	return false
}

// ConvertOfficeToPDFWithSpec converts an Office or iWork document to PDF using
// the selected (or auto-detected) local engine and commits the output atomically.
func ConvertOfficeToPDFWithSpec(ctx context.Context, spec OfficeConvertSpec) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(spec.InputFile) == "" {
		return "", ErrEmptyInput
	}
	engine, err := ParseOfficeEngine(string(spec.Engine))
	if err != nil {
		return "", err
	}
	inAbs, err := filepath.Abs(spec.InputFile)
	if err != nil {
		return "", fmt.Errorf("resolve input path: %w", err)
	}
	info, err := os.Stat(inAbs)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("%w: input document unavailable: %v", ErrConversionFailed, err)
	}

	status := DetectOfficeEngines(spec.SofficePath)
	ext := strings.ToLower(filepath.Ext(inAbs))
	backend, err := ResolveOfficeBackend(engine, ext, status, runtime.GOOS)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(spec.OutputDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	stageDir, err := os.MkdirTemp(spec.OutputDir, ".mtools-office-*")
	if err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)

	baseName := strings.TrimSuffix(filepath.Base(inAbs), filepath.Ext(inAbs))
	stagedPDF := filepath.Join(stageDir, baseName+".pdf")
	finalPDF := filepath.Join(spec.OutputDir, baseName+".pdf")

	switch backend {
	case OfficeEngineLibreOffice:
		err = convertWithLibreOffice(ctx, status.LibreOfficePath, inAbs, stageDir)
	case OfficeEngineMSOffice:
		err = convertWithMSOffice(ctx, inAbs, stagedPDF)
	case OfficeEngineAppleIWork:
		err = convertWithAppleIWork(ctx, inAbs, stagedPDF)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedEngine, backend)
	}
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	st, err := os.Stat(stagedPDF)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return "", fmt.Errorf("%w: output PDF not generated", ErrConversionFailed)
	}

	if err := replaceFileAtomic(stagedPDF, finalPDF); err != nil {
		return "", fmt.Errorf("%w: publish output PDF: %v", ErrConversionFailed, err)
	}
	return finalPDF, nil
}

func libreOfficeProfileURI(dir string) string {
	slash := filepath.ToSlash(filepath.Clean(dir))
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := url.URL{Scheme: "file", Path: slash}
	return u.String()
}

func convertWithLibreOffice(ctx context.Context, sofficePath, inAbs, stageDir string) error {
	profileDir := filepath.Join(stageDir, ".lo-profile")
	args := []string{
		"-env:UserInstallation=" + libreOfficeProfileURI(profileDir),
		"--headless",
		"--norestore",
		"--nofirststartwizard",
		"--convert-to", "pdf",
		"--outdir", stageDir,
		inAbs,
	}
	cmd := exec.CommandContext(ctx, sofficePath, args...)
	prepareCommand(cmd)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s (%v)", ErrConversionFailed, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func replaceFileAtomic(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	// On Windows os.Rename fails when dst already exists; remove and retry.
	if runtime.GOOS == "windows" {
		_ = os.Remove(dst)
		return os.Rename(src, dst)
	}
	return err
}

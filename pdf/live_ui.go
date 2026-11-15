package pdf

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/seiortech/mtools-sdk/job"
	"github.com/seiortech/mtools-sdk/pluginapi"
	"github.com/seiortech/mtools-sdk/ui/i18n"
)

//go:embed locales/*.json
var localesFS embed.FS

// LiveUI implements pluginapi.LiveUI for PDF Utility, rendering inside the
// main mtools window via UITree.
type LiveUI struct {
	mu      sync.Mutex
	catalog *i18n.Catalog
	lang    i18n.Lang

	activeTab string // "merge", "split", "images", "office"

	// Merge
	mergeFiles      []liveFileItem
	mergeOutputName string

	// Split
	splitFile       *liveFileItem
	splitMode       string // "all" or "range"
	splitPageRange  string
	splitOutputName string

	// Images
	imageFiles      []liveFileItem
	imageOutputName string

	// Office
	officeFiles []liveFileItem
	sofficePath string

	// Shared output & status
	outputDir   string
	inlineError string
	running     bool
	progress    float64
	progressMsg string
	resultMsg   string
}

type liveFileItem struct {
	Path      string
	Name      string
	SizeStr   string
	PageCount int
	AssetRef  string
}

// NewLiveUI creates a LiveUI instance for PDF Utility.
func NewLiveUI() *LiveUI {
	cat := i18n.NewCatalog()
	_ = cat.LoadFS(localesFS, "locales")
	return &LiveUI{
		catalog:         cat,
		lang:            i18n.LangEN,
		activeTab:       "merge",
		mergeOutputName: "merged.pdf",
		splitMode:       "all",
		splitOutputName: "extracted.pdf",
		imageOutputName: "images.pdf",
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

func (u *LiveUI) Open(ctx context.Context, params pluginapi.UIOpenParams, session *pluginapi.UISession) (pluginapi.UITree, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if params.Lang != "" {
		u.lang = i18n.NormalizeLang(params.Lang)
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

	return u.buildTreeLocked(0), nil
}

func (u *LiveUI) Event(ctx context.Context, ev pluginapi.UIEvent, session *pluginapi.UISession) error {
	u.mu.Lock()
	switch ev.Node {
	case "pdf.tabs":
		if ev.Kind == pluginapi.UIEventSelect && ev.Value != "" {
			u.activeTab = ev.Value
			u.inlineError = ""
			u.resultMsg = ""
		}
	case "pdf.merge.pick":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			for _, p := range splitPickedPaths(ev.Value) {
				u.mergeFiles = append(u.mergeFiles, u.makeItemLocked(ctx, p, session, false))
			}
			u.inlineError = ""
		}
	case "pdf.merge.outname":
		if ev.Kind == pluginapi.UIEventChange {
			u.mergeOutputName = ev.Value
		}
	case "pdf.split.pick":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			item := u.makeItemLocked(ctx, ev.Value, session, false)
			u.splitFile = &item
			u.inlineError = ""
		}
	case "pdf.split.mode":
		if ev.Kind == pluginapi.UIEventSelect && ev.Value != "" {
			u.splitMode = ev.Value
		}
	case "pdf.split.range":
		if ev.Kind == pluginapi.UIEventChange {
			u.splitPageRange = ev.Value
		}
	case "pdf.split.outname":
		if ev.Kind == pluginapi.UIEventChange {
			u.splitOutputName = ev.Value
		}
	case "pdf.images.pick":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			for _, p := range splitPickedPaths(ev.Value) {
				u.imageFiles = append(u.imageFiles, u.makeItemLocked(ctx, p, session, true))
			}
			u.inlineError = ""
		}
	case "pdf.images.outname":
		if ev.Kind == pluginapi.UIEventChange {
			u.imageOutputName = ev.Value
		}
	case "pdf.office.pick":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			for _, p := range splitPickedPaths(ev.Value) {
				u.officeFiles = append(u.officeFiles, u.makeItemLocked(ctx, p, session, false))
			}
			u.inlineError = ""
		}
	case "pdf.outdir":
		if ev.Kind == pluginapi.UIEventChange && ev.Value != "" {
			u.outputDir = ev.Value
		}
	case "pdf.run":
		if ev.Kind == pluginapi.UIEventClick && !u.running {
			u.startJobLocked(ctx, session)
		}
	default:
		u.handleListActionLocked(ev)
	}
	tree := u.buildTreeLocked(0)
	u.mu.Unlock()

	if session != nil {
		return session.Render(tree)
	}
	return nil
}

func (u *LiveUI) handleListActionLocked(ev pluginapi.UIEvent) {
	if ev.Kind != pluginapi.UIEventClick {
		return
	}
	var list *[]liveFileItem
	var prefix string
	switch {
	case strings.HasPrefix(ev.Node, "pdf.merge.item."):
		list = &u.mergeFiles
		prefix = "pdf.merge.item."
	case strings.HasPrefix(ev.Node, "pdf.images.item."):
		list = &u.imageFiles
		prefix = "pdf.images.item."
	case strings.HasPrefix(ev.Node, "pdf.office.item."):
		list = &u.officeFiles
		prefix = "pdf.office.item."
	default:
		return
	}
	rest := strings.TrimPrefix(ev.Node, prefix)
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 {
		return
	}
	idx, err := strconv.Atoi(parts[0])
	if err != nil || idx < 0 || idx >= len(*list) {
		return
	}
	s := *list
	switch parts[1] {
	case "up":
		if idx > 0 {
			s[idx-1], s[idx] = s[idx], s[idx-1]
		}
	case "down":
		if idx+1 < len(s) {
			s[idx+1], s[idx] = s[idx], s[idx+1]
		}
	case "rm":
		*list = append(s[:idx], s[idx+1:]...)
	}
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
	if strings.EqualFold(filepath.Ext(path), ".pdf") {
		if cnt, err := GetPageCount(ctx, path); err == nil {
			item.PageCount = cnt
		}
	}
	if copyImageAsset && session != nil && session.AssetsDir() != "" {
		if data, err := os.ReadFile(path); err == nil && len(data) <= 8<<20 {
			name := fmt.Sprintf("img_%d%s", len(u.imageFiles), strings.ToLower(filepath.Ext(path)))
			dst := filepath.Join(session.AssetsDir(), name)
			if os.WriteFile(dst, data, 0600) == nil {
				item.AssetRef = "asset:" + name
			}
		}
	}
	return item
}

func (u *LiveUI) startJobLocked(ctx context.Context, session *pluginapi.UISession) {
	var pdfJob PDFJob
	switch u.activeTab {
	case "merge":
		if len(u.mergeFiles) < 2 {
			u.inlineError = "Select at least two PDF files to merge."
			return
		}
		outName := strings.TrimSpace(u.mergeOutputName)
		if outName == "" {
			outName = "merged.pdf"
		}
		if !strings.HasSuffix(strings.ToLower(outName), ".pdf") {
			outName += ".pdf"
		}
		inFiles := make([]string, len(u.mergeFiles))
		for i, f := range u.mergeFiles {
			inFiles[i] = f.Path
		}
		pdfJob = PDFJob{
			Op:         OpMerge,
			InputFiles: inFiles,
			OutputFile: filepath.Join(u.outputDir, outName),
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
			outName := strings.TrimSpace(u.splitOutputName)
			if outName == "" {
				outName = "extracted.pdf"
			}
			if !strings.HasSuffix(strings.ToLower(outName), ".pdf") {
				outName += ".pdf"
			}
			pdfJob = PDFJob{
				Op:         OpExtract,
				InputFiles: []string{u.splitFile.Path},
				OutputFile: filepath.Join(u.outputDir, outName),
				PageRanges: ranges,
			}
		} else {
			pdfJob = PDFJob{
				Op:         OpSplit,
				InputFiles: []string{u.splitFile.Path},
				OutputDir:  u.outputDir,
				PageSpan:   1,
			}
		}
	case "images":
		if len(u.imageFiles) == 0 {
			u.inlineError = "Select at least one image."
			return
		}
		outName := strings.TrimSpace(u.imageOutputName)
		if outName == "" {
			outName = "images.pdf"
		}
		if !strings.HasSuffix(strings.ToLower(outName), ".pdf") {
			outName += ".pdf"
		}
		inFiles := make([]string, len(u.imageFiles))
		for i, f := range u.imageFiles {
			inFiles[i] = f.Path
		}
		pdfJob = PDFJob{
			Op:         OpImages,
			InputFiles: inFiles,
			OutputFile: filepath.Join(u.outputDir, outName),
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
		pdfJob = PDFJob{
			Op:          OpOffice,
			InputFiles:  inFiles,
			OutputDir:   u.outputDir,
			SofficePath: u.sofficePath,
		}
	default:
		return
	}

	u.running = true
	u.inlineError = ""
	u.resultMsg = ""
	u.progress = 5
	u.progressMsg = "Starting…"

	go func() {
		res, err := pdfJob.Run(ctx, func(pr job.Progress) {
			u.mu.Lock()
			u.progress = pr.Done
			u.progressMsg = pr.Message
			tree := u.buildTreeLocked(0)
			u.mu.Unlock()
			if session != nil {
				_ = session.Render(tree)
			}
		})
		u.mu.Lock()
		u.running = false
		if err != nil {
			u.inlineError = err.Error()
			u.progress = 0
			u.progressMsg = ""
		} else {
			u.progress = 100
			u.resultMsg = res.Summary
			if u.resultMsg == "" {
				u.resultMsg = "Completed."
			}
		}
		tree := u.buildTreeLocked(0)
		u.mu.Unlock()
		if session != nil {
			_ = session.Render(tree)
		}
	}()
}

func (u *LiveUI) buildTreeLocked(rev uint64) pluginapi.UITree {
	var body []pluginapi.UINode
	switch u.activeTab {
	case "merge":
		body = u.buildMergeBodyLocked()
	case "split":
		body = u.buildSplitBodyLocked()
	case "images":
		body = u.buildImagesBodyLocked()
	case "office":
		body = u.buildOfficeBodyLocked()
	}

	if u.inlineError != "" {
		body = append(body, liveNode("pdf.err", pluginapi.UINodeText, pluginapi.UITextProps{
			Text:    u.inlineError,
			Variant: "danger",
		}))
	}
	if u.running {
		body = append(body, liveNode("pdf.progress", pluginapi.UINodeProgress, pluginapi.UIProgressProps{
			Done:  u.progress,
			Total: 100,
			Label: u.progressMsg,
		}))
	}
	if u.resultMsg != "" {
		body = append(body, liveNode("pdf.result", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
			Padding: 12,
			Surface: "field_support",
			Border:  true,
		}, liveNode("pdf.result.txt", pluginapi.UINodeText, pluginapi.UITextProps{
			Text:    u.resultMsg,
			Variant: "body",
		})))
	}

	root := liveNode("pdf.root", pluginapi.UINodeScroll, pluginapi.UIContainerProps{
		Padding: 20,
		Gap:     16,
		Surface: "ground",
		Grow:    true,
	},
		liveNode("pdf.header", pluginapi.UINodeRow, pluginapi.UIContainerProps{
			Gap: 10,
		},
			liveNode("pdf.header.icon", pluginapi.UINodeIcon, pluginapi.UIIconProps{
				Name: "file-text",
				Size: 22,
				Tone: "accent",
			}),
			liveNode("pdf.header.title", pluginapi.UINodeText, pluginapi.UITextProps{
				Text:    u.t("title", "PDF Utility"),
				Variant: "title",
			}),
		),
		liveNode("pdf.tabs", pluginapi.UINodeTabs, pluginapi.UITabsProps{
			Active: u.activeTab,
			Tabs: []pluginapi.SurfaceOption{
				{Value: "merge", Label: u.t("tab_merge", "Merge")},
				{Value: "split", Label: u.t("tab_split", "Split")},
				{Value: "images", Label: u.t("tab_images", "Images to PDF")},
				{Value: "office", Label: u.t("tab_office", "Office to PDF")},
			},
		}),
		liveNode("pdf.card", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
			Padding: 16,
			Gap:     12,
			Surface: "paper",
			Border:  true,
		}, body...),
	)
	return pluginapi.UITree{Rev: rev, Root: root}
}

func (u *LiveUI) buildMergeBodyLocked() []pluginapi.UINode {
	nodes := []pluginapi.UINode{
		liveNode("pdf.merge.title", pluginapi.UINodeText, pluginapi.UITextProps{Text: "Merge PDF Files", Variant: "title"}),
		liveNode("pdf.merge.pick", pluginapi.UINodeFilePicker, pluginapi.UIFilePickerProps{
			Label:      "Add PDF Files",
			Mode:       "open",
			Extensions: []string{"pdf"},
			Multiple:   true,
			Disabled:   u.running,
		}),
	}
	nodes = append(nodes, u.buildFileListLocked("pdf.merge.item", u.mergeFiles, true)...)
	nodes = append(nodes,
		liveNode("pdf.merge.outlbl", pluginapi.UINodeText, pluginapi.UITextProps{Text: "Output PDF Name", Variant: "muted"}),
		liveNode("pdf.merge.outname", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{Value: u.mergeOutputName, Placeholder: "merged.pdf", Disabled: u.running}),
		liveNode("pdf.outdir", pluginapi.UINodeDirPicker, pluginapi.UIDirPickerProps{Label: "Save Destination", Value: u.outputDir, Disabled: u.running}),
		liveNode("pdf.run", pluginapi.UINodeButton, pluginapi.UIButtonProps{Label: "Merge PDFs", Variant: "primary", Icon: "file-text", Disabled: u.running}),
	)
	return nodes
}

func (u *LiveUI) buildSplitBodyLocked() []pluginapi.UINode {
	picked := ""
	info := "No PDF document selected."
	if u.splitFile != nil {
		picked = u.splitFile.Path
		if u.splitFile.PageCount > 0 {
			info = fmt.Sprintf("%s (%d pages)", u.splitFile.Name, u.splitFile.PageCount)
		} else {
			info = u.splitFile.Name
		}
	}
	nodes := []pluginapi.UINode{
		liveNode("pdf.split.title", pluginapi.UINodeText, pluginapi.UITextProps{Text: "Split PDF File", Variant: "title"}),
		liveNode("pdf.split.pick", pluginapi.UINodeFilePicker, pluginapi.UIFilePickerProps{
			Label:      "Select PDF File",
			Value:      picked,
			Mode:       "open",
			Extensions: []string{"pdf"},
			Disabled:   u.running,
		}),
		liveNode("pdf.split.info", pluginapi.UINodeText, pluginapi.UITextProps{Text: info, Variant: "muted"}),
		liveNode("pdf.split.mode", pluginapi.UINodeSelect, pluginapi.UISelectProps{
			Label:    "Split Method",
			Value:    u.splitMode,
			Disabled: u.running,
			Options: []pluginapi.SurfaceOption{
				{Value: "all", Label: "Separate All Pages"},
				{Value: "range", Label: "Extract Page Range"},
			},
		}),
	}
	if u.splitMode == "range" {
		nodes = append(nodes,
			liveNode("pdf.split.range", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{
				Value:       u.splitPageRange,
				Placeholder: "e.g. 1-3, 5, 8-10",
				Disabled:    u.running,
			}),
			liveNode("pdf.split.outname", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{
				Value:       u.splitOutputName,
				Placeholder: "extracted.pdf",
				Disabled:    u.running,
			}),
		)
	}
	nodes = append(nodes,
		liveNode("pdf.outdir", pluginapi.UINodeDirPicker, pluginapi.UIDirPickerProps{Label: "Save Destination", Value: u.outputDir, Disabled: u.running}),
		liveNode("pdf.run", pluginapi.UINodeButton, pluginapi.UIButtonProps{Label: "Split PDF", Variant: "primary", Icon: "scissors", Disabled: u.running}),
	)
	return nodes
}

func (u *LiveUI) buildImagesBodyLocked() []pluginapi.UINode {
	nodes := []pluginapi.UINode{
		liveNode("pdf.images.title", pluginapi.UINodeText, pluginapi.UITextProps{Text: "Convert Images to PDF", Variant: "title"}),
		liveNode("pdf.images.pick", pluginapi.UINodeFilePicker, pluginapi.UIFilePickerProps{
			Label:      "Add Images",
			Mode:       "open",
			Extensions: []string{"png", "jpg", "jpeg", "webp", "bmp"},
			Multiple:   true,
			Disabled:   u.running,
		}),
	}
	nodes = append(nodes, u.buildFileListLocked("pdf.images.item", u.imageFiles, true)...)
	nodes = append(nodes,
		liveNode("pdf.images.outname", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{Value: u.imageOutputName, Placeholder: "images.pdf", Disabled: u.running}),
		liveNode("pdf.outdir", pluginapi.UINodeDirPicker, pluginapi.UIDirPickerProps{Label: "Save Destination", Value: u.outputDir, Disabled: u.running}),
		liveNode("pdf.run", pluginapi.UINodeButton, pluginapi.UIButtonProps{Label: "Convert to PDF", Variant: "primary", Icon: "image", Disabled: u.running}),
	)
	return nodes
}

func (u *LiveUI) buildOfficeBodyLocked() []pluginapi.UINode {
	sofficeStatus := "LibreOffice detected."
	if _, err := FindSofficePath(u.sofficePath); err != nil {
		sofficeStatus = "LibreOffice (soffice) not found. Configure its path in Settings."
	}
	nodes := []pluginapi.UINode{
		liveNode("pdf.office.title", pluginapi.UINodeText, pluginapi.UITextProps{Text: "Convert Office Documents to PDF", Variant: "title"}),
		liveNode("pdf.office.status", pluginapi.UINodeText, pluginapi.UITextProps{Text: sofficeStatus, Variant: "muted"}),
		liveNode("pdf.office.pick", pluginapi.UINodeFilePicker, pluginapi.UIFilePickerProps{
			Label:      "Add Documents",
			Mode:       "open",
			Extensions: []string{"doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf"},
			Multiple:   true,
			Disabled:   u.running,
		}),
	}
	nodes = append(nodes, u.buildFileListLocked("pdf.office.item", u.officeFiles, false)...)
	nodes = append(nodes,
		liveNode("pdf.outdir", pluginapi.UINodeDirPicker, pluginapi.UIDirPickerProps{Label: "Save Destination", Value: u.outputDir, Disabled: u.running}),
		liveNode("pdf.run", pluginapi.UINodeButton, pluginapi.UIButtonProps{Label: "Convert Documents", Variant: "primary", Icon: "office", Disabled: u.running}),
	)
	return nodes
}

func (u *LiveUI) buildFileListLocked(prefix string, items []liveFileItem, reorderable bool) []pluginapi.UINode {
	if len(items) == 0 {
		return []pluginapi.UINode{
			liveNode(prefix+".empty", pluginapi.UINodeEmpty, pluginapi.UIEmptyProps{
				Title:    "No files added",
				Subtitle: "Use the file picker above to add files.",
			}),
		}
	}
	out := make([]pluginapi.UINode, 0, len(items))
	for i, it := range items {
		id := fmt.Sprintf("%s.%d", prefix, i)
		meta := it.SizeStr
		if it.PageCount > 0 {
			meta = fmt.Sprintf("%s · %d pages", it.SizeStr, it.PageCount)
		}
		var rowChildren []pluginapi.UINode
		if it.AssetRef != "" {
			rowChildren = append(rowChildren, liveNode(id+".thumb", pluginapi.UINodeImage, pluginapi.UIImageProps{
				Src: it.AssetRef, Fit: "contain", Width: 36, Height: 36,
			}))
		} else {
			rowChildren = append(rowChildren, liveNode(id+".ic", pluginapi.UINodeIcon, pluginapi.UIIconProps{
				Name: "file-text", Size: 18, Tone: "muted",
			}))
		}
		rowChildren = append(rowChildren,
			liveNode(id+".info", pluginapi.UINodeColumn, pluginapi.UIContainerProps{Gap: 2, Grow: true},
				liveNode(id+".name", pluginapi.UINodeText, pluginapi.UITextProps{Text: it.Name, Variant: "body"}),
				liveNode(id+".meta", pluginapi.UINodeText, pluginapi.UITextProps{Text: meta, Variant: "muted"}),
			),
		)
		if reorderable {
			rowChildren = append(rowChildren,
				liveNode(id+".up", pluginapi.UINodeButton, pluginapi.UIButtonProps{Label: "↑", Variant: "secondary", Disabled: u.running || i == 0}),
				liveNode(id+".down", pluginapi.UINodeButton, pluginapi.UIButtonProps{Label: "↓", Variant: "secondary", Disabled: u.running || i == len(items)-1}),
			)
		}
		rowChildren = append(rowChildren,
			liveNode(id+".rm", pluginapi.UINodeButton, pluginapi.UIButtonProps{Label: "Remove", Variant: "danger", Icon: "trash", Disabled: u.running}),
		)
		out = append(out, liveNode(id, pluginapi.UINodeRow, pluginapi.UIContainerProps{
			Padding: 8, Gap: 8, Surface: "ground", Border: true,
		}, rowChildren...))
	}
	return out
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

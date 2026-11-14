package pdf

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/seiortech/mtools-sdk/ui/i18n"
	"github.com/seiortech/mtools-sdk/job"
	plugin "github.com/seiortech/mtools-sdk/settings"
	"github.com/seiortech/mtools-sdk/ui/tool"
)

//go:embed locales/*.json
var localesFS embed.FS

const (
	PluginID tool.PluginID = "com.mtools.pdf"
	ToolID   tool.ToolID   = "pdf"
)

// PluginSettingsSchema returns declared settings schema for PDF Utility.
func PluginSettingsSchema() []plugin.SettingField {
	return []plugin.SettingField{
		{
			Key:         "output_dir",
			Type:        plugin.SettingDirectory,
			Label:       "Default Output Folder",
			Description: "Destination folder for generated and converted PDF files.",
			Default:     "~/Downloads",
			Required:    true,
		},
		{
			Key:         "soffice_path",
			Type:        plugin.SettingFile,
			Label:       "LibreOffice executable path",
			Description: "Custom soffice path for Office to PDF conversions. Leave empty to auto-detect.",
			Default:     "",
			Required:    false,
			Placeholder: "/Applications/LibreOffice.app/Contents/MacOS/soffice",
			Filter: &plugin.SettingFileFilter{
				Name:       "Executable",
				Extensions: []string{"", "exe"},
			},
			Action: &plugin.SettingAction{
				ID:          "get_libreoffice",
				Label:       "Get LibreOffice",
				Description: "Open official LibreOffice download page in browser.",
			},
		},
	}
}

// Plugin implements the native PDF Utility tool.
type Plugin struct {
	tool.PluginPaths
	store          plugin.SettingsStore
	queue          *job.Queue
	state          *ViewState
	history        *HistoryStore
	catalog        *i18n.Catalog
	langResolver   func() i18n.Lang
	externalRunner func(j PDFJob) (job.Job, bool)
}

// LocaleTranslator implements tool.LocaleProvider.
func (p *Plugin) LocaleTranslator() i18n.Translator {
	return p.catalog
}

// SetLanguageResolver implements tool.LocaleProvider.
func (p *Plugin) SetLanguageResolver(fn func() i18n.Lang) {
	p.langResolver = fn
}

func (p *Plugin) effectiveLang() i18n.Lang {
	if p.store != nil {
		if raw := p.store.GetString("language"); raw != "" && raw != "global" {
			return i18n.NormalizeLang(raw)
		}
	}
	if p.langResolver != nil {
		return p.langResolver()
	}
	return i18n.CurrentLang()
}

func (p *Plugin) t(key string) string {
	if p.catalog == nil {
		return key
	}
	return p.catalog.T(p.effectiveLang(), key)
}

// SetExternalRunner configures a runner that delegates PDF jobs to an external bundle.
func (p *Plugin) SetExternalRunner(runner func(j PDFJob) (job.Job, bool)) {
	p.externalRunner = runner
}

func (p *Plugin) enqueueJob(title string, j PDFJob) job.JobID {
	if p.queue == nil {
		return ""
	}
	if p.externalRunner != nil {
		if extJob, ok := p.externalRunner(j); ok && extJob != nil {
			return p.queue.Enqueue(title, extJob)
		}
	}
	return p.queue.Enqueue(title, j)
}

// NewPlugin creates a PDF Utility plugin instance.
func NewPlugin(dataDir string, store plugin.SettingsStore, queue *job.Queue) *Plugin {
	paths := tool.NewPluginPaths(dataDir)
	if store == nil {
		store, _ = plugin.NewJSONSettingsStore(paths.GetSettingsPath(), PluginSettingsSchema())
	}
	cat, err := i18n.NewCatalogFromFS(localesFS, "locales")
	if err != nil {
		cat = i18n.NewCatalog()
	}
	if dataDir != "" {
		_ = cat.LoadDir(filepath.Join(dataDir, "i18n"))
	}
	return &Plugin{
		PluginPaths: paths,
		store:       store,
		queue:       queue,
		state:       NewViewState(),
		history:     NewHistoryStore(dataDir),
		catalog:     cat,
	}
}

// History returns the plugin history store.
func (p *Plugin) History() *HistoryStore {
	if p.history == nil {
		p.history = NewHistoryStore(p.GetDefaultPath())
	}
	return p.history
}

// HistoryEntries implements tool.HistoryProvider for global history.
func (p *Plugin) HistoryEntries() []tool.HistoryEntry {
	items := p.History().All()
	entries := make([]tool.HistoryEntry, len(items))
	for i, it := range items {
		subtitle := it.CompletedAt.Format("Jan 02, 15:04")
		if it.FileCount > 0 {
			subtitle = fmt.Sprintf("%d files · %s", it.FileCount, subtitle)
		}
		entries[i] = tool.HistoryEntry{
			ID:        it.ID,
			ToolID:    ToolID,
			ToolName:  p.t("pdf.name"),
			Title:     it.Summary,
			Subtitle:  subtitle,
			FilePath:  it.OutputFile,
			Timestamp: it.CompletedAt,
		}
	}
	return entries
}

// DeleteHistory removes an item by ID from the history store.
func (p *Plugin) DeleteHistory(id string) error {
	return p.History().Delete(id)
}

// ClearHistory removes all items from the history store.
func (p *Plugin) ClearHistory() error {
	return p.History().ClearAll()
}

// Meta returns tool metadata.
func (p *Plugin) Meta() tool.ToolMeta {
	return tool.ToolMeta{
		ID:          ToolID,
		PluginID:    PluginID,
		Name:        p.t("pdf.name"),
		Category:    "Document",
		Description: p.t("pdf.desc"),
		Icon:        "file-text",
	}
}

// Settings returns the settings store.
func (p *Plugin) Settings() plugin.SettingsStore {
	return p.store
}

// RunAction executes plugin-level action buttons.
func (p *Plugin) RunAction(ctx context.Context, actionID string) error {
	switch actionID {
	case "get_libreoffice":
		url := "https://www.libreoffice.org/download/download-libreoffice/"
		return mygo.Shell.OpenExternal(url)
	default:
		return fmt.Errorf("unknown action: %s", actionID)
	}
}

// RevealFile reveals a file in the system file manager.
func RevealFile(filePath string) {
	mygo.Shell.ShowItemInFolder(filePath)
}

// OpenFile opens a file with default system handler.
func OpenFile(filePath string) error {
	return mygo.Shell.OpenPath(filePath)
}

// View mounts the native workspace UI.
func (p *Plugin) View(c *ui.Context) {
	p.RenderWorkspace(c)
}

// OnOpen implements tool.WorkspaceLifecycle.
func (p *Plugin) OnOpen() {
	if p.state == nil {
		p.state = NewViewState()
	}
	_ = p.EnsureDirs()
}

// HasActiveJob implements tool.WorkspaceLifecycle.
func (p *Plugin) HasActiveJob() bool {
	if p.queue == nil || p.state == nil || p.state.ActiveJobID == "" {
		return false
	}
	rec, ok := p.queue.GetRecord(p.state.ActiveJobID)
	return ok && (rec.Status == job.StatusQueued || rec.Status == job.StatusRunning)
}

// OnClose implements tool.WorkspaceLifecycle.
func (p *Plugin) OnClose() {
	if p.queue != nil && p.state != nil && p.state.ActiveJobID != "" {
		p.queue.Cancel(p.state.ActiveJobID)
	}
	p.state = NewViewState()
	_ = p.CleanTemp()
}

package pdf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/seiortech/mtools-sdk/job"
	"github.com/seiortech/mtools-sdk/ui/components"
	"github.com/seiortech/mtools-sdk/ui/theme"
)

type TabMode int

const (
	TabMerge TabMode = iota
	TabSplit
	TabImages
	TabOffice
)

func (m TabMode) String() string {
	switch m {
	case TabMerge:
		return "Merge"
	case TabSplit:
		return "Split"
	case TabImages:
		return "Images to PDF"
	case TabOffice:
		return "Office to PDF"
	default:
		return "PDF"
	}
}

// FileItem represents a staged file in the PDF workspace.
type FileItem struct {
	Path      string
	Name      string
	SizeStr   string
	PageCount int
	Thumbnail ui.ImageSource
}

func makeFileItem(path string) FileItem {
	info, err := os.Stat(path)
	sizeStr := ""
	if err == nil {
		sizeStr = formatFileSize(info.Size())
	}
	return FileItem{
		Path:      path,
		Name:      filepath.Base(path),
		SizeStr:   sizeStr,
		PageCount: -1,
	}
}

func formatFileSize(bytes int64) string {
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

func (p *Plugin) loadThumbnailAsync(path string, redraw func()) {
	if path == "" {
		return
	}
	go func() {
		var img ui.ImageSource
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".bmp" {
			data, err := os.ReadFile(path)
			if err == nil {
				img, _ = ui.DecodeImage(data)
			}
		}
		if img == nil {
			iconBytes, err := mygo.Shell.FileIcon(path, 64)
			if err == nil && len(iconBytes) > 0 {
				img, _ = ui.DecodeImage(iconBytes)
			}
		}
		if img == nil {
			return
		}

		if p.state.SplitFile != nil && p.state.SplitFile.Path == path {
			p.state.SplitFile.Thumbnail = img
		}
		for i := range p.state.MergeFiles {
			if p.state.MergeFiles[i].Path == path {
				p.state.MergeFiles[i].Thumbnail = img
			}
		}
		for i := range p.state.ImageFiles {
			if p.state.ImageFiles[i].Path == path {
				p.state.ImageFiles[i].Thumbnail = img
			}
		}
		for i := range p.state.OfficeFiles {
			if p.state.OfficeFiles[i].Path == path {
				p.state.OfficeFiles[i].Thumbnail = img
			}
		}
		redraw()
	}()
}

// ViewState tracks transient UI state across frames.
type ViewState struct {
	ActiveTab TabMode

	// Merge state
	MergeFiles      []FileItem
	MergeOutputName string

	// Split state
	SplitFile    *FileItem
	SplitExtract bool
	SplitRange   string

	// Images state
	ImageFiles      []FileItem
	ImageOutputName string

	// Office state
	OfficeFiles []FileItem

	// Active job & messages
	ActiveJobID    job.JobID
	RecordedJobIDs map[job.JobID]bool
	InlineError    string
	ToastMessage   string
	ToastTimer     *time.Timer

	// History panel visibility & size
	HistoryOpen  bool
	HistoryInit  bool
	HistoryWidth int
}

// NewViewState returns initialized ViewState with sensible defaults.
func NewViewState() *ViewState {
	return &ViewState{
		ActiveTab:       TabMerge,
		MergeOutputName: "merged_document.pdf",
		ImageOutputName: "images_combined.pdf",
		SplitRange:      "1-3",
		RecordedJobIDs:  make(map[job.JobID]bool),
		HistoryWidth:    320,
	}
}

// RenderWorkspace draws the native PDF Utility workspace.
func (p *Plugin) RenderWorkspace(c *ui.Context) {
	redraw := c.Invalidate

	w, _ := c.Size()
	if !p.state.HistoryInit {
		p.state.HistoryOpen = w >= 1150
		p.state.HistoryInit = true
	}
	if p.state.HistoryWidth <= 0 {
		p.state.HistoryWidth = 320
	}

	if p.state.HistoryOpen {
		ui.Row(c).Fill().Children(func() {
			ui.Scroll(c).
				Grow(1).
				FillHeight().
				Padding(24, 32).
				Children(func() {
					p.renderWorkspaceContent(c, redraw)
				})

			components.VerticalResizeHandle(c, components.ResizeHandleProps{
				Width:        p.state.HistoryWidth,
				MinWidth:     260,
				MaxWidth:     600,
				DefaultWidth: 320,
				OnResize: func(newW int) {
					p.state.HistoryWidth = newW
					redraw()
				},
			})

			ui.Column(c).
				Width(float32(p.state.HistoryWidth)).
				FillHeight().
				BorderWidth(0, 0, 0, 2).
				BorderColor(theme.Ink).
				Background(theme.Paper).
				Children(func() {
					p.renderHistoryPanel(c, redraw)
				})
		})
	} else {
		ui.Scroll(c).
			Fill().
			Padding(24, 32).
			Children(func() {
				p.renderWorkspaceContent(c, redraw)
			})
	}
}

func (p *Plugin) renderWorkspaceContent(c *ui.Context, redraw func()) {
	ui.Column(c).
		FillWidth().
		Gap(24).
		Children(func() {
			// 1. Header & Tab Navigation
			ui.Column(c).Gap(12).Children(func() {
				ui.Row(c).Wrap().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Box(c).
							Size(40, 40).
							Background(theme.FieldSupport).
							Border(2, theme.Ink).
							Justify(ui.Center).
							AlignItems(ui.Center).
							Children(func() {
								ui.Icon(c, components.IconFileText).Size(20, 20).TextColor(theme.Ink)
							})

						ui.Column(c).Gap(2).Children(func() {
							ui.Text(c, p.t("pdf.workbench")).
								FontSize(18).
								FontWeight(700).
								TextColor(theme.Ink)

							ui.Text(c, "Fast, native document operations. Zero cloud dependency.").
								FontSize(12).
								TextColor(theme.InkMuted)
						})
					})

					ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
						if p.state.ToastMessage != "" {
							ui.Box(c).
								Padding(6, 12).
								Background(theme.Paper).
								Border(2, theme.Ink).
								Shadow(2, 2, 0, 0, theme.Ink).
								Children(func() {
									ui.Text(c, p.state.ToastMessage).
										FontSize(12).
										FontWeight(600).
										TextColor(theme.Ink)
								})
						}

						histLabel := fmt.Sprintf("History (%d)", len(p.History().All()))
						if p.state.HistoryOpen {
							histLabel = "Hide History"
						}
						components.SecondaryButton(c, histLabel, func() {
							p.state.HistoryOpen = !p.state.HistoryOpen
							redraw()
						})
					})
				})

				// Tabs strip
				ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
					p.renderTabButton(c, p.t("pdf.tab_merge"), components.IconLayers, TabMerge, redraw)
					p.renderTabButton(c, p.t("pdf.tab_split"), components.IconScissors, TabSplit, redraw)
					p.renderTabButton(c, p.t("pdf.tab_image"), components.IconImage, TabImages, redraw)
					p.renderTabButton(c, p.t("pdf.tab_office"), components.IconOfficeDoc, TabOffice, redraw)
				})
			})

			// 2. Active Tab Form Card
			ui.Column(c).
				Border(2, theme.Ink).
				Background(theme.Paper).
				Padding(24).
				Gap(20).
				Children(func() {
					switch p.state.ActiveTab {
					case TabMerge:
						p.renderMergeTab(c, redraw)
					case TabSplit:
						p.renderSplitTab(c, redraw)
					case TabImages:
						p.renderImagesTab(c, redraw)
					case TabOffice:
						p.renderOfficeTab(c, redraw)
					}
				})

			// 3. Active Task Live Card
			if p.state.ActiveJobID != "" && p.queue != nil {
				if rec, ok := p.queue.GetRecord(p.state.ActiveJobID); ok {
					p.renderJobCard(c, rec, redraw)
				}
			}
		})
}

func (p *Plugin) renderTabButton(c *ui.Context, label string, icon *ui.SVG, mode TabMode, redraw func()) {
	isActive := p.state.ActiveTab == mode
	bg := theme.Paper
	textCol := theme.Ink
	if isActive {
		bg = theme.Accent
		textCol = theme.OnField
	}

	b := ui.ButtonBase(c).
		Height(34).
		Padding(0, 14).
		Radius(0).
		Border(2, theme.Ink).
		Background(bg).
		Justify(ui.Center).
		AlignItems(ui.Center).
		Label(label).
		Cursor(ui.CursorPointer)

	b.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if icon != nil {
				ui.Icon(c, icon).Size(14, 14).TextColor(textCol)
			}
			ui.Text(c, label).
				FontSize(12).
				FontWeight(700).
				TextColor(textCol)
		})
	})

	if b.Clicked() {
		p.state.ActiveTab = mode
		p.state.InlineError = ""
		redraw()
	}
}

// --- MERGE TAB ---

func (p *Plugin) renderMergeTab(c *ui.Context, redraw func()) {
	ui.Column(c).Gap(16).Children(func() {
		ui.Row(c).Wrap().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Gap(4).MinWidth(200).Grow(1).Children(func() {
				ui.Text(c, "Merge PDF Files").FontSize(15).FontWeight(700).TextColor(theme.Ink)
				ui.Text(c, "Combine multiple PDF documents into one single document in exact order.").
					FontSize(12).TextColor(theme.InkMuted)
			})

			ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
				components.PrimaryButton(c, "+ Add PDF Files...", func() {
					go func() {
						paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
							Multiple: true,
							Filters: []mygo.FileFilter{
								{Name: "PDF Documents", Extensions: []string{"pdf"}},
							},
						})
						if err == nil && len(paths) > 0 {
							for _, path := range paths {
								p.state.MergeFiles = append(p.state.MergeFiles, makeFileItem(path))
								p.loadThumbnailAsync(path, redraw)
								go func(filePath string) {
									cnt, countErr := GetPageCount(context.Background(), filePath)
									if countErr == nil {
										for i := range p.state.MergeFiles {
											if p.state.MergeFiles[i].Path == filePath {
												p.state.MergeFiles[i].PageCount = cnt
											}
										}
										redraw()
									}
								}(path)
							}
							p.state.InlineError = ""
							redraw()
						}
					}()
				})

				if len(p.state.MergeFiles) > 0 {
					components.SecondaryButton(c, "Clear All", func() {
						p.state.MergeFiles = nil
						redraw()
					})
				}
			})
		})

		// File list
		if len(p.state.MergeFiles) == 0 {
			p.renderEmptyDropZone(c, "No PDF files selected. Click '+ Add PDF Files...' to get started.")
		} else {
			p.renderFileList(c, p.state.MergeFiles, func(idx int) {
				// Move Up
				if idx > 0 {
					p.state.MergeFiles[idx], p.state.MergeFiles[idx-1] = p.state.MergeFiles[idx-1], p.state.MergeFiles[idx]
					redraw()
				}
			}, func(idx int) {
				// Move Down
				if idx < len(p.state.MergeFiles)-1 {
					p.state.MergeFiles[idx], p.state.MergeFiles[idx+1] = p.state.MergeFiles[idx+1], p.state.MergeFiles[idx]
					redraw()
				}
			}, func(idx int) {
				// Remove
				p.state.MergeFiles = append(p.state.MergeFiles[:idx], p.state.MergeFiles[idx+1:]...)
				redraw()
			})
		}

		// Output filename and destination
		p.renderOutputForm(c, &p.state.MergeOutputName, "merged_document.pdf", redraw)

		// Submit action
		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			label := fmt.Sprintf("Merge %d Files", len(p.state.MergeFiles))
			if len(p.state.MergeFiles) <= 1 {
				label = "Merge Files"
			}
			components.PrimaryButton(c, label, func() {
				p.startMergeJob(redraw)
			})

			if p.state.InlineError != "" {
				ui.Text(c, p.state.InlineError).FontSize(12).FontWeight(600).TextColor(theme.Danger)
			}
		})
	})
}

// --- SPLIT TAB ---

func (p *Plugin) renderSplitTab(c *ui.Context, redraw func()) {
	ui.Column(c).Gap(16).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "Split PDF File").FontSize(15).FontWeight(700).TextColor(theme.Ink)
			ui.Text(c, "Extract single pages or split into individual PDF documents.").
				FontSize(12).TextColor(theme.InkMuted)
		})

		// Single file selector
		ui.Box(c).
			Border(2, theme.Ink).
			Background(theme.Ground).
			Padding(16).
			Children(func() {
				ui.Row(c).Wrap().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
					if p.state.SplitFile == nil {
						ui.Text(c, "No PDF document selected.").FontSize(13).TextColor(theme.InkMuted)
					} else {
						ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Box(c).
								Size(48, 48).
								Background(theme.Paper).
								Border(1, theme.Ink).
								Justify(ui.Center).
								AlignItems(ui.Center).
								Clip().
								Children(func() {
									if p.state.SplitFile.Thumbnail != nil {
										ui.Image(c, p.state.SplitFile.Thumbnail).Size(48, 48)
									} else {
										ui.Icon(c, components.IconFileText).Size(22, 22).TextColor(theme.InkMuted)
									}
								})

							ui.Column(c).Gap(2).Children(func() {
								ui.Text(c, p.state.SplitFile.Name).FontSize(13).FontWeight(700).TextColor(theme.Ink)
								pgText := "reading pages..."
								if p.state.SplitFile.PageCount >= 0 {
									pgText = fmt.Sprintf("%d pages · %s", p.state.SplitFile.PageCount, p.state.SplitFile.SizeStr)
								}
								ui.Text(c, pgText).FontSize(11).TextColor(theme.InkMuted)
							})
						})
					}

					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						if p.state.SplitFile != nil {
							components.SecondaryButton(c, "Open File", func() {
								_ = OpenFile(p.state.SplitFile.Path)
							})
						}
						components.SecondaryButton(c, "Choose PDF File...", func() {
							go func() {
								paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
									Filters: []mygo.FileFilter{
										{Name: "PDF Documents", Extensions: []string{"pdf"}},
									},
								})
								if err == nil && len(paths) > 0 {
									item := makeFileItem(paths[0])
									p.state.SplitFile = &item
									p.loadThumbnailAsync(paths[0], redraw)
									cnt, _ := GetPageCount(context.Background(), paths[0])
									if p.state.SplitFile != nil && p.state.SplitFile.Path == paths[0] {
										p.state.SplitFile.PageCount = cnt
									}
									p.state.InlineError = ""
									redraw()
								}
							}()
						})
					})
				})
			})

		// Split Mode selection
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "Split Method").FontSize(13).FontWeight(600).TextColor(theme.Ink)

			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				allBg := theme.Paper
				allText := theme.Ink
				if !p.state.SplitExtract {
					allBg = theme.Accent
					allText = theme.OnField
				}
				bAll := ui.ButtonBase(c).Height(32).Padding(0, 14).Radius(0).Border(2, theme.Ink).Background(allBg).
					Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("All Pages (Separate PDFs)")
				bAll.Children(func() {
					ui.Text(c, "Separate All Pages").FontSize(12).FontWeight(600).TextColor(allText)
				})
				if bAll.Clicked() {
					p.state.SplitExtract = false
					redraw()
				}

				rangeBg := theme.Paper
				rangeText := theme.Ink
				if p.state.SplitExtract {
					rangeBg = theme.Accent
					rangeText = theme.OnField
				}
				bRange := ui.ButtonBase(c).Height(32).Padding(0, 14).Radius(0).Border(2, theme.Ink).Background(rangeBg).
					Justify(ui.Center).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("Extract Page Range")
				bRange.Children(func() {
					ui.Text(c, "Extract Page Range").FontSize(12).FontWeight(600).TextColor(rangeText)
				})
				if bRange.Clicked() {
					p.state.SplitExtract = true
					redraw()
				}
			})
		})

		if p.state.SplitExtract {
			ui.Column(c).Gap(6).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Page Selection").FontSize(13).FontWeight(600).TextColor(theme.Ink)
					ui.Text(c, "(e.g. 1-3, 5, 8-10)").FontSize(11).TextColor(theme.InkMuted)
				})
				ui.TextInput(c, &p.state.SplitRange).
					Placeholder("1-3, 5").
					Height(36).
					Radius(0).
					Padding(0, 12).
					Border(2, theme.Ink).
					Background(theme.Paper).
					TextColor(theme.Ink).
					FillWidth()
			})
		}

		// Output destination
		p.renderOutputDirRow(c, redraw)

		// Submit action
		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			actionLabel := "Split PDF"
			if p.state.SplitExtract {
				actionLabel = "Extract Pages"
			}
			components.PrimaryButton(c, actionLabel, func() {
				p.startSplitJob(redraw)
			})

			if p.state.InlineError != "" {
				ui.Text(c, p.state.InlineError).FontSize(12).FontWeight(600).TextColor(theme.Danger)
			}
		})
	})
}

// --- IMAGES TO PDF TAB ---

func (p *Plugin) renderImagesTab(c *ui.Context, redraw func()) {
	ui.Column(c).Gap(16).Children(func() {
		ui.Row(c).Wrap().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Gap(4).MinWidth(200).Grow(1).Children(func() {
				ui.Text(c, "Convert Images to PDF").FontSize(15).FontWeight(700).TextColor(theme.Ink)
				ui.Text(c, "Compile PNG, JPEG, TIFF, or WebP images into a single PDF document.").
					FontSize(12).TextColor(theme.InkMuted)
			})

			ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
				components.PrimaryButton(c, "+ Add Images...", func() {
					go func() {
						paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
							Multiple: true,
							Filters: []mygo.FileFilter{
								{Name: "Images", Extensions: []string{"png", "jpg", "jpeg", "webp", "tif", "tiff"}},
							},
						})
						if err == nil && len(paths) > 0 {
							for _, path := range paths {
								p.state.ImageFiles = append(p.state.ImageFiles, makeFileItem(path))
								p.loadThumbnailAsync(path, redraw)
							}
							p.state.InlineError = ""
							redraw()
						}
					}()
				})

				if len(p.state.ImageFiles) > 0 {
					components.SecondaryButton(c, "Clear All", func() {
						p.state.ImageFiles = nil
						redraw()
					})
				}
			})
		})

		// File list
		if len(p.state.ImageFiles) == 0 {
			p.renderEmptyDropZone(c, "No image files selected. Click '+ Add Images...' to select photos or scans.")
		} else {
			p.renderFileList(c, p.state.ImageFiles, func(idx int) {
				if idx > 0 {
					p.state.ImageFiles[idx], p.state.ImageFiles[idx-1] = p.state.ImageFiles[idx-1], p.state.ImageFiles[idx]
					redraw()
				}
			}, func(idx int) {
				if idx < len(p.state.ImageFiles)-1 {
					p.state.ImageFiles[idx], p.state.ImageFiles[idx+1] = p.state.ImageFiles[idx+1], p.state.ImageFiles[idx]
					redraw()
				}
			}, func(idx int) {
				p.state.ImageFiles = append(p.state.ImageFiles[:idx], p.state.ImageFiles[idx+1:]...)
				redraw()
			})
		}

		// Output filename and destination
		p.renderOutputForm(c, &p.state.ImageOutputName, "images_combined.pdf", redraw)

		// Submit action
		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			components.PrimaryButton(c, fmt.Sprintf("Convert %d Images to PDF", len(p.state.ImageFiles)), func() {
				p.startImagesJob(redraw)
			})

			if p.state.InlineError != "" {
				ui.Text(c, p.state.InlineError).FontSize(12).FontWeight(600).TextColor(theme.Danger)
			}
		})
	})
}

// --- OFFICE TO PDF TAB ---

func (p *Plugin) renderOfficeTab(c *ui.Context, redraw func()) {
	sofficePath, sofficeErr := FindSofficePath(p.store.GetString("soffice_path"))
	hasOffice := sofficeErr == nil

	ui.Column(c).Gap(16).Children(func() {
		ui.Row(c).Wrap().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Column(c).Gap(4).MinWidth(200).Grow(1).Children(func() {
				ui.Text(c, "Convert Office Documents to PDF").FontSize(15).FontWeight(700).TextColor(theme.Ink)
				ui.Text(c, "Convert Word (.docx, .doc), Excel (.xlsx, .xls), and PowerPoint (.pptx) to PDF.").
					FontSize(12).TextColor(theme.InkMuted)
			})

			if hasOffice {
				ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
					components.PrimaryButton(c, "+ Add Documents...", func() {
						go func() {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
								Multiple: true,
								Filters: []mygo.FileFilter{
									{Name: "Office Documents", Extensions: []string{"docx", "doc", "xlsx", "xls", "pptx", "ppt"}},
								},
							})
							if err == nil && len(paths) > 0 {
								for _, path := range paths {
									p.state.OfficeFiles = append(p.state.OfficeFiles, makeFileItem(path))
									p.loadThumbnailAsync(path, redraw)
								}
								p.state.InlineError = ""
								redraw()
							}
						}()
					})

					if len(p.state.OfficeFiles) > 0 {
						components.SecondaryButton(c, "Clear All", func() {
							p.state.OfficeFiles = nil
							redraw()
						})
					}
				})
			}
		})

		// Engine Status Banner
		if hasOffice {
			ui.Box(c).
				Border(1, theme.Ink).
				Background(theme.FieldSupport).
				Padding(10, 14).
				Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "✓ LibreOffice engine ready:").
							FontSize(12).
							FontWeight(700).
							TextColor(theme.Ink)

						ui.Text(c, sofficePath).
							FontSize(11).
							TextColor(theme.Ink).
							SingleLine()
					})
				})
		} else {
			ui.Box(c).
				Border(2, theme.Danger).
				Background(theme.Paper).
				Padding(14, 16).
				Children(func() {
					ui.Row(c).Wrap().Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(12).Children(func() {
						ui.Column(c).Gap(4).MinWidth(220).Grow(1).Children(func() {
							ui.Text(c, "▲ LibreOffice Required").
								FontSize(13).
								FontWeight(700).
								TextColor(theme.Danger)

							ui.Text(c, "Word and Excel conversions require LibreOffice's headless CLI installed on your system.").
								FontSize(12).
								TextColor(theme.Ink)
						})

						ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
							components.PrimaryButton(c, "Get LibreOffice", func() {
								_ = p.RunAction(context.Background(), "get_libreoffice")
							})

							components.SecondaryButton(c, "Locate Binary...", func() {
								go func() {
									paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
										Filters: []mygo.FileFilter{
											{Name: "Executable", Extensions: []string{"", "exe"}},
										},
									})
									if err == nil && len(paths) > 0 {
										p.store.Set("soffice_path", paths[0])
										_ = p.store.Save()
										redraw()
									}
								}()
							})
						})
					})
				})
		}

		// File list
		if len(p.state.OfficeFiles) == 0 {
			p.renderEmptyDropZone(c, "No documents selected. Click '+ Add Documents...' to convert.")
		} else {
			p.renderFileList(c, p.state.OfficeFiles, func(idx int) {
				if idx > 0 {
					p.state.OfficeFiles[idx], p.state.OfficeFiles[idx-1] = p.state.OfficeFiles[idx-1], p.state.OfficeFiles[idx]
					redraw()
				}
			}, func(idx int) {
				if idx < len(p.state.OfficeFiles)-1 {
					p.state.OfficeFiles[idx], p.state.OfficeFiles[idx+1] = p.state.OfficeFiles[idx+1], p.state.OfficeFiles[idx]
					redraw()
				}
			}, func(idx int) {
				p.state.OfficeFiles = append(p.state.OfficeFiles[:idx], p.state.OfficeFiles[idx+1:]...)
				redraw()
			})
		}

		// Output destination
		p.renderOutputDirRow(c, redraw)

		// Submit action
		if hasOffice {
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				components.PrimaryButton(c, fmt.Sprintf("Convert %d Documents to PDF", len(p.state.OfficeFiles)), func() {
					p.startOfficeJob(sofficePath, redraw)
				})

				if p.state.InlineError != "" {
					ui.Text(c, p.state.InlineError).FontSize(12).FontWeight(600).TextColor(theme.Danger)
				}
			})
		}
	})
}

// --- COMMON UI RENDERERS ---

func (p *Plugin) renderEmptyDropZone(c *ui.Context, message string) {
	ui.Box(c).
		FillWidth().
		Height(110).
		Border(2, theme.Ink).
		Background(theme.Ground).
		Justify(ui.Center).
		AlignItems(ui.Center).
		Children(func() {
			ui.Text(c, message).
				FontSize(13).
				FontWeight(600).
				TextColor(theme.InkMuted)
		})
}

func (p *Plugin) renderFileList(c *ui.Context, items []FileItem, onUp, onDown, onRemove func(int)) {
	ui.Column(c).
		FillWidth().
		Border(2, theme.Ink).
		Background(theme.Ground).
		Children(func() {
			for i, it := range items {
				idx := i
				item := it
				ui.Row(c).
					FillWidth().
					Padding(10, 14).
					BorderWidth(0, 0, 1, 0).
					BorderColor(theme.Ink).
					Justify(ui.SpaceBetween).
					AlignItems(ui.Center).
					Children(func() {
						ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, fmt.Sprintf("%d.", idx+1)).
								FontSize(12).
								FontWeight(700).
								TextColor(theme.InkMuted)

							// 36x36 Thumbnail / Preview square
							ui.Box(c).
								Size(36, 36).
								Border(1, theme.Ink).
								Background(theme.Paper).
								Justify(ui.Center).
								AlignItems(ui.Center).
								Clip().
								Children(func() {
									if item.Thumbnail != nil {
										ui.Image(c, item.Thumbnail).Size(36, 36)
									} else {
										fallbackIcon := components.IconFileText
										if p.state.ActiveTab == TabImages {
											fallbackIcon = components.IconImage
										} else if p.state.ActiveTab == TabOffice {
											fallbackIcon = components.IconOfficeDoc
										}
										ui.Icon(c, fallbackIcon).Size(18, 18).TextColor(theme.InkMuted)
									}
								})

							ui.Column(c).Gap(2).Children(func() {
								ui.Text(c, item.Name).
									FontSize(13).
									FontWeight(600).
									TextColor(theme.Ink)

								metaStr := item.SizeStr
								if item.PageCount > 0 {
									metaStr = fmt.Sprintf("%d pages · %s", item.PageCount, item.SizeStr)
								}
								if metaStr != "" {
									ui.Text(c, metaStr).FontSize(11).TextColor(theme.InkMuted)
								}
							})
						})

						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							if idx > 0 {
								components.SecondaryButton(c, "↑", func() { onUp(idx) })
							}
							if idx < len(items)-1 {
								components.SecondaryButton(c, "↓", func() { onDown(idx) })
							}
							components.SecondaryButton(c, "✕", func() { onRemove(idx) })
						})
					})
			}
		})
}

func (p *Plugin) renderOutputForm(c *ui.Context, nameRef *string, placeholder string, redraw func()) {
	ui.Column(c).Gap(12).Children(func() {
		// Output Filename
		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "Output PDF Name").FontSize(13).FontWeight(600).TextColor(theme.Ink)
			ui.TextInput(c, nameRef).
				Placeholder(placeholder).
				Height(36).
				Radius(0).
				Padding(0, 12).
				Border(2, theme.Ink).
				Background(theme.Paper).
				TextColor(theme.Ink).
				FillWidth()
		})

		// Output Directory
		p.renderOutputDirRow(c, redraw)
	})
}

func (p *Plugin) renderOutputDirRow(c *ui.Context, redraw func()) {
	outDir := p.store.GetString("output_dir")
	displayDir := outDir
	if displayDir == "" {
		displayDir = "~/Downloads"
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, "Save Destination").FontSize(13).FontWeight(600).TextColor(theme.Ink)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, displayDir).
				FontSize(13).
				TextColor(theme.Ink).
				Grow(1)

			components.SecondaryButton(c, "Change...", func() {
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Directory: true})
					if err == nil && len(paths) > 0 {
						p.store.Set("output_dir", paths[0])
						_ = p.store.Save()
						redraw()
					}
				}()
			})

			if outDir != "" && outDir != "~/Downloads" {
				components.SecondaryButton(c, "Reset to Default", func() {
					p.store.Set("output_dir", "~/Downloads")
					_ = p.store.Save()
					redraw()
				})
			}
		})
	})
}

// --- JOB CARD & HISTORY ---

func (p *Plugin) renderJobCard(c *ui.Context, rec job.JobRecord, redraw func()) {
	// Record finished jobs to history
	if (rec.Status == job.StatusSucceeded || rec.Status == job.StatusFailed) && !p.state.RecordedJobIDs[rec.ID] {
		p.state.RecordedJobIDs[rec.ID] = true
		if rec.Status == job.StatusSucceeded {
			outPath := ""
			if len(rec.Result.Outputs) > 0 {
				outPath = rec.Result.Outputs[0]
			}
			_ = p.History().Add(HistoryItem{
				ID:          string(rec.ID),
				Operation:   p.state.ActiveTab.String(),
				Summary:     rec.Result.Summary,
				OutputFile:  outPath,
				FileCount:   len(rec.Result.Outputs),
				CompletedAt: time.Now(),
			})
		}
	}

	ui.Column(c).
		Border(2, theme.Ink).
		Background(theme.Paper).
		Padding(20).
		Gap(12).
		Children(func() {
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Task Activity").
					FontSize(15).
					FontWeight(700).
					TextColor(theme.Ink)

				statusLabel := "RUNNING"
				statusBg := theme.Accent
				switch rec.Status {
				case job.StatusSucceeded:
					statusLabel = "DONE"
					statusBg = theme.Success
				case job.StatusFailed:
					statusLabel = "FAILED"
					statusBg = theme.Danger
				case job.StatusCanceled:
					statusLabel = "CANCELED"
					statusBg = theme.Warning
				}

				ui.Box(c).
					Padding(2, 8).
					Border(1, theme.Ink).
					Background(statusBg).
					Children(func() {
						ui.Text(c, statusLabel).
							FontSize(11).
							FontWeight(700).
							TextColor(theme.OnStatus)
					})
			})

			// Progress bar
			components.ProgressBar(c, rec.Progress.Done, 12)

			// Metrics and Actions
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				msg := rec.Progress.Message
				if rec.Status == job.StatusFailed && rec.Error != "" {
					msg = rec.Error
				}
				if rec.Status == job.StatusSucceeded && rec.Result.Summary != "" {
					msg = rec.Result.Summary
				}
				ui.Text(c, msg).
					FontSize(12).
					FontWeight(600).
					TextColor(theme.Ink).
					Grow(1)

				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if rec.Status == job.StatusRunning {
						components.SecondaryButton(c, "Cancel", func() {
							_ = p.queue.Cancel(rec.ID)
							redraw()
						})
					} else if rec.Status == job.StatusSucceeded && len(rec.Result.Outputs) > 0 {
						components.SecondaryButton(c, "Reveal in Folder", func() {
							RevealFile(rec.Result.Outputs[0])
						})
						components.PrimaryButton(c, "Open PDF", func() {
							_ = OpenFile(rec.Result.Outputs[0])
						})
					}
				})
			})
		})
}

func (p *Plugin) renderHistoryPanel(c *ui.Context, redraw func()) {
	items := p.History().All()
	cardItems := make([]components.HistoryCardItem, len(items))
	for i, it := range items {
		cardItems[i] = components.HistoryCardItem{
			ID:       it.ID,
			Title:    it.Summary,
			Subtitle: it.CompletedAt.Format("Jan 02, 15:04"),
			FilePath: it.OutputFile,
		}
	}

	components.HistoryPanel(c, components.HistoryPanelProps{
		Title:         "PDF History",
		Count:         len(items),
		Items:         cardItems,
		ToastMessage:  p.state.ToastMessage,
		EmptyTitle:    "No past operations.",
		EmptySubtitle: "Completed tasks will appear here.",
		ShowHide:      true,
		OnHide: func() {
			p.state.HistoryOpen = false
			redraw()
		},
		OnClearAll: func() {
			_ = p.History().ClearAll()
			redraw()
		},
		OnDismissToast: func() {
			p.state.ToastMessage = ""
			if p.state.ToastTimer != nil {
				p.state.ToastTimer.Stop()
			}
			redraw()
		},
		OnShow: func(filePath string) {
			fi, err := os.Stat(filePath)
			if err != nil {
				p.showToast("File was moved or deleted", redraw)
				return
			}
			if !fi.IsDir() {
				RevealFile(filePath)
			} else {
				_ = OpenFile(filePath)
			}
		},
		OnDelete: func(id string) {
			_ = p.History().Delete(id)
			redraw()
		},
	})
}

// --- JOB STARTERS ---

func (p *Plugin) resolveOutputDir() string {
	out := p.store.GetString("output_dir")
	if out == "" || out == "~/Downloads" {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, "Downloads")
		}
		return os.TempDir()
	}
	if strings.HasPrefix(out, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, out[2:])
		}
	}
	return out
}

func (p *Plugin) startMergeJob(redraw func()) {
	if len(p.state.MergeFiles) == 0 {
		p.state.InlineError = "Please add at least one PDF file."
		redraw()
		return
	}

	p.state.InlineError = ""
	outDir := p.resolveOutputDir()
	fn := strings.TrimSpace(p.state.MergeOutputName)
	if fn == "" {
		fn = "merged_document.pdf"
	}
	if !strings.HasSuffix(strings.ToLower(fn), ".pdf") {
		fn += ".pdf"
	}
	outPath := filepath.Join(outDir, fn)

	var inPaths []string
	for _, f := range p.state.MergeFiles {
		inPaths = append(inPaths, f.Path)
	}

	j := PDFJob{
		Op:         OpMerge,
		InputFiles: inPaths,
		OutputFile: outPath,
	}

	p.state.ActiveJobID = p.enqueueJob("Merge PDFs", j)
	p.showToast("Merge job started", redraw)
}

func (p *Plugin) startSplitJob(redraw func()) {
	if p.state.SplitFile == nil {
		p.state.InlineError = "Please select a PDF file to split."
		redraw()
		return
	}

	p.state.InlineError = ""
	outDir := p.resolveOutputDir()

	if p.state.SplitExtract {
		r := strings.TrimSpace(p.state.SplitRange)
		if r == "" {
			p.state.InlineError = "Please enter page selection (e.g. 1-3, 5)."
			redraw()
			return
		}
		base := strings.TrimSuffix(p.state.SplitFile.Name, filepath.Ext(p.state.SplitFile.Name))
		outPath := filepath.Join(outDir, base+"_extracted.pdf")
		var parts []string
		for _, part := range strings.Split(r, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				parts = append(parts, trimmed)
			}
		}

		j := PDFJob{
			Op:         OpExtract,
			InputFiles: []string{p.state.SplitFile.Path},
			OutputFile: outPath,
			PageRanges: parts,
		}
		p.state.ActiveJobID = p.enqueueJob("Extract PDF Pages", j)
	} else {
		j := PDFJob{
			Op:         OpSplit,
			InputFiles: []string{p.state.SplitFile.Path},
			OutputDir:  outDir,
			PageSpan:   1,
		}
		p.state.ActiveJobID = p.enqueueJob("Split PDF", j)
	}
	p.showToast("Split job started", redraw)
}

func (p *Plugin) startImagesJob(redraw func()) {
	if len(p.state.ImageFiles) == 0 {
		p.state.InlineError = "Please add at least one image file."
		redraw()
		return
	}

	p.state.InlineError = ""
	outDir := p.resolveOutputDir()
	fn := strings.TrimSpace(p.state.ImageOutputName)
	if fn == "" {
		fn = "images_combined.pdf"
	}
	if !strings.HasSuffix(strings.ToLower(fn), ".pdf") {
		fn += ".pdf"
	}
	outPath := filepath.Join(outDir, fn)

	var inPaths []string
	for _, f := range p.state.ImageFiles {
		inPaths = append(inPaths, f.Path)
	}

	j := PDFJob{
		Op:         OpImages,
		InputFiles: inPaths,
		OutputFile: outPath,
	}

	p.state.ActiveJobID = p.enqueueJob("Images to PDF", j)
	p.showToast("Images to PDF job started", redraw)
}

func (p *Plugin) startOfficeJob(sofficePath string, redraw func()) {
	if len(p.state.OfficeFiles) == 0 {
		p.state.InlineError = "Please add at least one Office document."
		redraw()
		return
	}

	p.state.InlineError = ""
	outDir := p.resolveOutputDir()

	var inPaths []string
	for _, f := range p.state.OfficeFiles {
		inPaths = append(inPaths, f.Path)
	}

	j := PDFJob{
		Op:          OpOffice,
		InputFiles:  inPaths,
		OutputDir:   outDir,
		SofficePath: sofficePath,
	}

	p.state.ActiveJobID = p.enqueueJob("Convert Office to PDF", j)
	p.showToast("Conversion job started", redraw)
}

func (p *Plugin) showToast(msg string, redraw func()) {
	p.state.ToastMessage = msg
	if p.state.ToastTimer != nil {
		p.state.ToastTimer.Stop()
	}
	p.state.ToastTimer = time.AfterFunc(3*time.Second, func() {
		p.state.ToastMessage = ""
		redraw()
	})
	redraw()
}

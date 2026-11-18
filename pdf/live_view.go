package pdf

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	pdfeng "github.com/seiortech/mtools-plugin-pdf/internal/pdf"
	"github.com/seiortech/mtools-sdk/pluginapi"
)

func (u *LiveUI) buildTreeLocked() pluginapi.UITree {
	u.rev++
	splitW := u.railWidth
	if u.railCollapsed {
		splitW = 48
	}
	if splitW <= 0 {
		splitW = defaultRailWidth
	}

	root := liveNode("pdf.root", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
		Fill:      true,
		Surface:   "ground",
		Shortcuts: []string{"Escape", "ArrowLeft", "ArrowRight", "=", "-", "0"},
	},
		u.buildTopBarLocked(),
		liveNode("pdf.main_split", pluginapi.UINodeSplit, pluginapi.UISplitProps{
			Axis:               "h",
			Size:               splitW,
			Min:                260,
			Max:                440,
			DefaultSize:        defaultRailWidth,
			Collapsible:        true,
			Collapsed:          u.railCollapsed,
			ResetOnDoubleClick: true,
		},
			u.buildPreviewPaneLocked(),
			u.buildRightPanelLocked(),
		),
	)
	return pluginapi.UITree{Rev: u.rev, Root: root}
}

func (u *LiveUI) buildTopBarLocked() pluginapi.UINode {
	leftGroup := liveNode("pdf.topbar.left", pluginapi.UINodeRow, pluginapi.UIContainerProps{
		Gap:   10,
		Align: "center",
		Grow:  true,
	},
		liveNode("pdf.tabs", pluginapi.UINodeTabs, pluginapi.UITabsProps{
			Active: u.activeTab,
			Tabs: []pluginapi.SurfaceOption{
				{Value: "merge", Label: u.t("pdf.tab_merge", "Merge")},
				{Value: "split", Label: u.t("pdf.tab_split", "Split")},
				{Value: "images", Label: u.t("pdf.tab_images", "Images to PDF")},
				{Value: "office", Label: u.t("pdf.tab_office", "Office to PDF")},
			},
		}),
	)

	var centerChildren []pluginapi.UINode
	totalPages := len(u.previewPages)
	if totalPages > 0 {
		centerChildren = append(centerChildren,
			liveNode("pdf.page_stepper", pluginapi.UINodeRow, pluginapi.UIContainerProps{
				Surface:     "ground",
				BorderSize:  1,
				PaddingH:    4,
				PaddingV:    2,
				Gap:         4,
				Align:       "center",
				NoFillWidth: true,
			},
				liveNode("pdf.page_prev", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Icon:     "chevron-left",
					IconOnly: true,
					Variant:  "ghost",
					Height:   28,
					Width:    28,
					Disabled: u.currentPage <= 1,
				}),
				liveNode("pdf.page_readout", pluginapi.UINodeText, pluginapi.UITextProps{
					Text:        fmt.Sprintf("%d / %d", u.currentPage, totalPages),
					FontSize:    12,
					FontWeight:  600,
					SingleLine:  true,
					TabularNums: true,
					PaddingH:    6,
				}),
				liveNode("pdf.page_next", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Icon:     "chevron-right",
					IconOnly: true,
					Variant:  "ghost",
					Height:   28,
					Width:    28,
					Disabled: u.currentPage >= totalPages,
				}),
			),
			liveNode("pdf.zoom_box", pluginapi.UINodeRow, pluginapi.UIContainerProps{
				Surface:     "ground",
				BorderSize:  1,
				PaddingH:    4,
				PaddingV:    2,
				Gap:         4,
				Align:       "center",
				NoFillWidth: true,
			},
				liveNode("pdf.zoom_out", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Icon:     "zoom-out",
					IconOnly: true,
					Variant:  "ghost",
					Height:   28,
					Width:    28,
					Disabled: u.zoom <= minZoomFactor+0.01,
				}),
				liveNode("pdf.zoom_readout", pluginapi.UINodeText, pluginapi.UITextProps{
					Text:        fmt.Sprintf("%.0f%%", u.effectiveZoomLocked()*100),
					FontSize:    12,
					FontWeight:  600,
					SingleLine:  true,
					TabularNums: true,
					PaddingH:    4,
				}),
				liveNode("pdf.zoom_in", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Icon:     "zoom-in",
					IconOnly: true,
					Variant:  "ghost",
					Height:   28,
					Width:    28,
					Disabled: u.zoom >= maxZoomFactor-0.01,
				}),
				liveNode("pdf.zoom_fit", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Label:   u.t("pdf.zoom_fit", "Fit"),
					Variant: "secondary",
					Height:  28,
				}),
			),
		)
	}
	centerGroup := liveNode("pdf.topbar.center", pluginapi.UINodeRow, pluginapi.UIContainerProps{
		Gap:         8,
		Align:       "center",
		NoFillWidth: true,
	}, centerChildren...)

	toggleIcon := "collapse-right"
	toggleTip := u.t("pdf.panel_collapse", "Collapse Panel")
	if u.railCollapsed {
		toggleIcon = "collapse-left"
		toggleTip = u.t("pdf.panel_expand", "Expand Panel")
	}

	var rightChildren []pluginapi.UINode
	if u.running {
		rightChildren = append(rightChildren, liveNode("pdf.cancel", pluginapi.UINodeButton, pluginapi.UIButtonProps{
			Label:   u.t("pdf.btn_cancel", "Cancel"),
			Variant: "danger",
			Icon:    "close",
		}))
	} else {
		rightChildren = append(rightChildren, u.buildPrimaryRunButtonLocked())
	}
	rightChildren = append(rightChildren, liveNode("pdf.rail_toggle", pluginapi.UINodeButton, pluginapi.UIButtonProps{
		Icon:     toggleIcon,
		IconOnly: true,
		Variant:  "secondary",
		Width:    32,
		Height:   32,
		Tooltip:  toggleTip,
	}))

	rightGroup := liveNode("pdf.topbar.right", pluginapi.UINodeRow, pluginapi.UIContainerProps{
		Gap:         8,
		Align:       "center",
		NoFillWidth: true,
	}, rightChildren...)

	return liveNode("pdf.topbar", pluginapi.UINodeRow, pluginapi.UIContainerProps{
		Height:      52,
		PaddingH:    16,
		Justify:     "between",
		Align:       "center",
		Surface:     "paper",
		BorderSides: [4]int{0, 0, 2, 0},
	}, leftGroup, centerGroup, rightGroup)
}

func (u *LiveUI) buildPrimaryRunButtonLocked() pluginapi.UINode {
	label, icon := u.runActionMetaLocked()
	return liveNode("pdf.run", pluginapi.UINodeButton, pluginapi.UIButtonProps{
		Label:    label,
		Variant:  "primary",
		Icon:     icon,
		Disabled: !u.canRunLocked(),
	})
}

func (u *LiveUI) runActionMetaLocked() (label, icon string) {
	switch u.activeTab {
	case "merge":
		return u.t("pdf.btn_merge", "Merge PDFs"), "file-text"
	case "split":
		if u.splitMode == "range" {
			return u.t("pdf.btn_extract", "Extract Pages"), "scissors"
		}
		return u.t("pdf.btn_split", "Split PDF"), "scissors"
	case "images":
		return u.t("pdf.btn_images", "Convert to PDF"), "image"
	case "office":
		return u.t("pdf.btn_office", "Convert Documents"), "office"
	default:
		return "Run", "file-text"
	}
}

func (u *LiveUI) buildPreviewPaneLocked() pluginapi.UINode {
	if len(u.previewPages) == 0 {
		if u.activeTab == "office" && len(u.officeFiles) > 0 {
			return u.buildOfficePreviewLedgerLocked()
		}
		return u.buildEmptyPreviewLocked()
	}

	z := u.effectiveZoomLocked()
	includedMap := map[int]bool{}
	if u.activeTab == "split" && u.splitMode == "range" {
		includedMap = parseIncludedPages(u.splitPageRange, len(u.previewPages))
	}
	splitSpan := 1
	if u.activeTab == "split" && u.splitMode == "all" {
		if n, err := strconv.Atoi(strings.TrimSpace(u.splitSpan)); err == nil && n > 0 {
			splitSpan = n
		}
	}

	pageNodes := make([]pluginapi.UINode, 0, len(u.previewPages))
	for _, pg := range u.previewPages {
		pw := min(max(int(math.Round(pg.W*z)), 100), 8192)
		ph := min(max(int(math.Round(pg.H*z)), 100), 8192)

		if absInt(pg.GlobalPage-u.currentPage) > 4 {
			pageNodes = append(pageNodes, liveNode(fmt.Sprintf("page_spacer_%d", pg.GlobalPage), pluginapi.UINodeColumn, pluginapi.UIContainerProps{
				Width:       pw,
				Height:      ph,
				Surface:     "paper",
				BorderSize:  1,
				NoFillWidth: true,
				Justify:     "center",
				Align:       "center",
			},
				liveNode(fmt.Sprintf("page_spacer_lbl_%d", pg.GlobalPage), pluginapi.UINodeText, pluginapi.UITextProps{
					Text:    fmt.Sprintf("Page %d", pg.GlobalPage),
					Variant: "muted",
				}),
			))
			continue
		}

		assetRef := u.pageAssetRefLocked(pg)
		var canvasChildren []pluginapi.UINode
		if assetRef == "" {
			loadingText := u.t("pdf.rendering_page", "Rendering page...")
			loadingVariant := "muted"
			for ek, emsg := range u.previewRenderErr {
				if ek.docGen == u.docGen && ek.sourcePath == pg.SourcePath && ek.pageNum == pg.PDFPage && emsg != "" {
					loadingText = u.t("pdf.preview_render_failed", "Page preview failed to render")
					loadingVariant = "danger"
					break
				}
			}
			canvasChildren = append(canvasChildren, liveNode(fmt.Sprintf("page_loading_%d", pg.GlobalPage), pluginapi.UINodeColumn, pluginapi.UIContainerProps{
				Width:       pw,
				Height:      ph,
				Justify:     "center",
				Align:       "center",
				NoFillWidth: true,
			},
				liveNode(fmt.Sprintf("page_loading_txt_%d", pg.GlobalPage), pluginapi.UINodeText, pluginapi.UITextProps{
					Text:    loadingText,
					Variant: loadingVariant,
				}),
			))
		}

		borderW := 1
		borderToken := "ink"
		if u.activeTab == "split" && u.splitMode == "range" && includedMap[pg.GlobalPage] {
			borderW = 3
			borderToken = "accent"
			leftVal := 8.0
			topVal := 8.0
			canvasChildren = append(canvasChildren, liveNode(fmt.Sprintf("page_sel_%d", pg.GlobalPage), pluginapi.UINodeRow, pluginapi.UIContainerProps{
				Absolute:    true,
				Left:        &leftVal,
				Top:         &topVal,
				PaddingH:    6,
				PaddingV:    2,
				Surface:     "accent",
				NoFillWidth: true,
			},
				liveNode(fmt.Sprintf("page_sel_txt_%d", pg.GlobalPage), pluginapi.UINodeText, pluginapi.UITextProps{
					Text:       u.t("pdf.extract_badge", "EXTRACT"),
					Variant:    "on_field",
					FontSize:   10,
					FontWeight: 700,
				}),
			))
		} else if u.activeTab == "split" && u.splitMode == "all" {
			chunkIdx := ((pg.GlobalPage - 1) / splitSpan) + 1
			leftVal := 8.0
			topVal := 8.0
			canvasChildren = append(canvasChildren, liveNode(fmt.Sprintf("page_chunk_%d", pg.GlobalPage), pluginapi.UINodeRow, pluginapi.UIContainerProps{
				Absolute:    true,
				Left:        &leftVal,
				Top:         &topVal,
				PaddingH:    6,
				PaddingV:    2,
				Surface:     "field_support",
				BorderSize:  1,
				NoFillWidth: true,
			},
				liveNode(fmt.Sprintf("page_chunk_txt_%d", pg.GlobalPage), pluginapi.UINodeText, pluginapi.UITextProps{
					Text:       fmt.Sprintf("#%d", chunkIdx),
					FontSize:   10,
					FontWeight: 700,
				}),
			))
		}

		badgeText := fmt.Sprintf("%d", pg.GlobalPage)
		if u.activeTab == "merge" && len(u.mergeFiles) > 1 {
			badgeText = fmt.Sprintf("%s · %d", pg.SourceName, pg.PDFPage)
		}
		rightVal := 8.0
		bottomVal := 8.0
		canvasChildren = append(canvasChildren, liveNode(fmt.Sprintf("page_badge_%d", pg.GlobalPage), pluginapi.UINodeRow, pluginapi.UIContainerProps{
			Absolute:    true,
			Right:       &rightVal,
			Bottom:      &bottomVal,
			PaddingH:    6,
			PaddingV:    2,
			Surface:     "paper",
			BorderSize:  1,
			NoFillWidth: true,
		},
			liveNode(fmt.Sprintf("page_badge_txt_%d", pg.GlobalPage), pluginapi.UINodeText, pluginapi.UITextProps{
				Text:     badgeText,
				FontSize: 10,
				Variant:  "muted",
			}),
		))

		pageNodes = append(pageNodes, liveNode(fmt.Sprintf("page_%d", pg.GlobalPage), pluginapi.UINodeCanvas, pluginapi.UICanvasProps{
			Width:       pw,
			Height:      ph,
			Src:         assetRef,
			Surface:     "paper",
			BorderWidth: borderW,
			BorderToken: borderToken,
			Shadow:      [2]int{6, 6},
			Clickable:   true,
		}, canvasChildren...))
	}

	viewportNode := liveNode("pdf.viewport", pluginapi.UINodeScroll, pluginapi.UIContainerProps{
		Fill:        true,
		Grow:        true,
		Padding:     24,
		Gap:         24,
		Align:       "center",
		Justify:     "center",
		Surface:     "ground",
		ScrollBoth:  true,
		TrackScroll: true,
		ScrollTarget: &pluginapi.UIScrollTarget{
			Y:      u.targetScrollY,
			Seq:    u.scrollSeq,
			Smooth: u.scrollSeq > 1,
		},
	}, pageNodes...)

	if u.previewEngineErr == nil {
		return viewportNode
	}

	titleKey := "pdf.pdfium_missing_title"
	fallbackTitle := "PDFium preview engine not configured"
	if errors.Is(u.previewEngineErr, pdfeng.ErrPDFiumInvalid) {
		titleKey = "pdf.pdfium_invalid_title"
		fallbackTitle = "PDFium preview engine failed to load"
	}
	banner := liveNode("pdfium_engine_banner", pluginapi.UINodeRow, pluginapi.UIContainerProps{
		Surface:     "paper",
		BorderSize:  1,
		BorderToken: "danger",
		Padding:     12,
		Gap:         12,
		Align:       "center",
	},
		liveNode("pdfium_engine_msg_col", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
			Grow: true,
			Gap:  2,
		},
			liveNode("pdfium_engine_title", pluginapi.UINodeText, pluginapi.UITextProps{
				Text:    u.t(titleKey, fallbackTitle),
				Variant: "danger",
			}),
			liveNode("pdfium_engine_desc", pluginapi.UINodeText, pluginapi.UITextProps{
				Text:    u.t("pdf.pdfium_missing_desc", "Open App Settings -> PDFium WebAssembly (pdfium.wasm), click Download pdfium.wasm or select pdfium.wasm, then click Reload Preview."),
				Variant: "muted",
			}),
		),
		liveNode("pdfium_engine_actions", pluginapi.UINodeRow, pluginapi.UIContainerProps{
			Gap:         8,
			Align:       "center",
			NoFillWidth: true,
		},
			liveNode("download_pdfium", pluginapi.UINodeButton, pluginapi.UIButtonProps{
				Label:          u.t("pdf.btn_download_pdfium", "Download pdfium.wasm"),
				Variant:        "primary",
				Icon:           "download",
				HostAction:     "run_app_setting_action",
				HostSettingKey: "pdfium_wasm_path",
			}),
			liveNode("open_settings", pluginapi.UINodeButton, pluginapi.UIButtonProps{
				Label:      u.t("pdf.btn_open_settings", "Open App Settings"),
				Variant:    "secondary",
				Icon:       "settings",
				HostAction: "open_app_settings",
			}),
			liveNode("retry_pdfium", pluginapi.UINodeButton, pluginapi.UIButtonProps{
				Label:   u.t("pdf.btn_reload_preview", "Reload Preview"),
				Variant: "secondary",
				Icon:    "refresh-cw",
			}),
		),
	)

	return liveNode("pdf.viewport_with_banner", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
		Fill: true,
		Grow: true,
	}, banner, viewportNode)
}

func (u *LiveUI) buildEmptyPreviewLocked() pluginapi.UINode {
	var title, desc, btnLabel, icon, pickID string
	var exts []string
	var multi bool

	switch u.activeTab {
	case "merge":
		title = u.t("pdf.empty_merge_title", "Merge PDF Documents")
		desc = u.t("pdf.empty_merge_desc", "Add two or more PDFs to preview pages in order and combine them into one file.")
		btnLabel = u.t("pdf.btn_add_pdfs", "Add PDFs...")
		icon = "file-text"
		pickID = "pdf.merge.empty_pick"
		exts = []string{"pdf"}
		multi = true
	case "split":
		title = u.t("pdf.empty_split_title", "Split or Extract PDF Pages")
		desc = u.t("pdf.empty_split_desc", "Select a PDF document to preview all pages, split into chunks, or click pages to extract.")
		btnLabel = u.t("pdf.btn_select_pdf", "Choose PDF...")
		icon = "scissors"
		pickID = "pdf.split.empty_pick"
		exts = []string{"pdf"}
	case "images":
		title = u.t("pdf.empty_images_title", "Convert Images to PDF")
		desc = u.t("pdf.empty_images_desc", "Add PNG, JPEG, WebP, or BMP images to preview and arrange them into a PDF.")
		btnLabel = u.t("pdf.btn_add_images", "Add Images...")
		icon = "image"
		pickID = "pdf.images.empty_pick"
		exts = []string{"png", "jpg", "jpeg", "webp", "bmp"}
		multi = true
	case "office":
		title = u.t("pdf.empty_office_title", "Convert Office Documents")
		desc = u.t("pdf.empty_office_desc", "Add Word, Excel, PowerPoint, or OpenDocument files to convert locally via LibreOffice.")
		btnLabel = u.t("pdf.btn_add_office", "Add Documents...")
		icon = "office"
		pickID = "pdf.office.empty_pick"
		exts = []string{"doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf"}
		multi = true
	}

	return liveNode("pdf.empty_wrap", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
		Fill:    true,
		Surface: "ground",
		Justify: "center",
		Align:   "center",
		Padding: 24,
	},
		liveNode(pickID, pluginapi.UINodeEmpty, pluginapi.UIEmptyProps{
			Title:       title,
			Subtitle:    desc,
			ActionLabel: btnLabel,
			Icon:        icon,
			IconSize:    36,
			Width:       440,
			BorderWidth: 3,
			Shadow:      [2]int{5, 5},
			PickFile: &pluginapi.UIFilePickerProps{
				Label:      btnLabel,
				Mode:       "open",
				Extensions: exts,
				Multiple:   multi,
			},
		}),
	)
}

func (u *LiveUI) buildOfficePreviewLedgerLocked() pluginapi.UINode {
	cards := []pluginapi.UINode{
		liveNode("pdf.office.preview_hint", pluginapi.UINodeText, pluginapi.UITextProps{
			Text:    u.t("pdf.office_preview_hint", "Office documents are rendered by LibreOffice during conversion. Click Convert Documents to generate and preview the PDF."),
			Variant: "muted",
		}),
	}
	for i, it := range u.officeFiles {
		cards = append(cards, liveNode(fmt.Sprintf("pdf.office.card.%d", i), pluginapi.UINodeRow, pluginapi.UIContainerProps{
			Padding: 14,
			Gap:     12,
			Align:   "center",
			Surface: "paper",
			Border:  true,
			Shadow:  [2]int{4, 4},
		},
			liveNode(fmt.Sprintf("pdf.office.card.%d.ic", i), pluginapi.UINodeIcon, pluginapi.UIIconProps{
				Name: "office",
				Size: 24,
				Tone: "accent",
			}),
			liveNode(fmt.Sprintf("pdf.office.card.%d.info", i), pluginapi.UINodeColumn, pluginapi.UIContainerProps{
				Gap:  2,
				Grow: true,
			},
				liveNode(fmt.Sprintf("pdf.office.card.%d.name", i), pluginapi.UINodeText, pluginapi.UITextProps{
					Text:       it.Name,
					FontWeight: 600,
				}),
				liveNode(fmt.Sprintf("pdf.office.card.%d.meta", i), pluginapi.UINodeText, pluginapi.UITextProps{
					Text:    it.SizeStr,
					Variant: "muted",
				}),
			),
		))
	}
	return liveNode("pdf.office.preview_scroll", pluginapi.UINodeScroll, pluginapi.UIContainerProps{
		Fill:    true,
		Padding: 24,
		Gap:     12,
		Surface: "ground",
	}, cards...)
}

func (u *LiveUI) buildRightPanelLocked() pluginapi.UINode {
	if u.railCollapsed {
		return liveNode("pdf.rail_collapsed", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
			Width:       48,
			FillHeight:  true,
			PaddingV:    12,
			Gap:         12,
			Align:       "center",
			Surface:     "paper",
			BorderSides: [4]int{0, 0, 0, 2},
		},
			liveNode("pdf.rail_expand", pluginapi.UINodeButton, pluginapi.UIButtonProps{
				Icon:     "collapse-left",
				IconOnly: true,
				Variant:  "ghost",
				Width:    32,
				Height:   32,
				Tooltip:  u.t("pdf.panel_expand", "Expand Panel"),
			}),
			liveNode("pdf.rail_ic", pluginapi.UINodeIcon, pluginapi.UIIconProps{
				Name: "file-text",
				Size: 18,
				Tone: "accent",
			}),
			liveNode("pdf.rail_pg_badge", pluginapi.UINodeText, pluginapi.UITextProps{
				Text:        fmt.Sprintf("%d/%d", u.currentPage, max(len(u.previewPages), 1)),
				FontSize:    11,
				FontWeight:  600,
				TabularNums: true,
			}),
		)
	}

	var sections []pluginapi.UINode
	if banner := u.buildStatusBannerLocked(); banner != nil {
		sections = append(sections, *banner)
	}
	sections = append(sections, u.buildModeControlsLocked()...)
	if len(u.previewPages) > 1 {
		sections = append(sections, u.buildPageJumpLocked())
	}

	return liveNode("pdf.right_panel", pluginapi.UINodeScroll, pluginapi.UIContainerProps{
		FillHeight:  true,
		Padding:     14,
		Gap:         14,
		Surface:     "paper",
		BorderSides: [4]int{0, 0, 0, 2},
	}, sections...)
}

func (u *LiveUI) buildStatusBannerLocked() *pluginapi.UINode {
	if u.running {
		n := liveNode("pdf.banner.running", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
			Padding:    12,
			Gap:        8,
			Surface:    "ground",
			BorderSize: 2,
		},
			liveNode("pdf.progress", pluginapi.UINodeProgress, pluginapi.UIProgressProps{
				Done:  u.progress,
				Total: 100,
				Label: u.progressMsg,
			}),
		)
		return &n
	}
	if u.inlineError != "" {
		n := liveNode("pdf.err", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
			Padding:     12,
			Gap:         8,
			Surface:     "paper",
			BorderSize:  2,
			BorderToken: "danger",
		},
			liveNode("pdf.err.txt", pluginapi.UINodeText, pluginapi.UITextProps{
				Text:    u.inlineError,
				Variant: "danger",
			}),
			liveNode("pdf.dismiss_banner", pluginapi.UINodeButton, pluginapi.UIButtonProps{
				Label:   u.t("pdf.btn_dismiss", "Dismiss"),
				Variant: "secondary",
				Height:  28,
			}),
		)
		return &n
	}
	if u.resultMsg != "" {
		children := []pluginapi.UINode{
			liveNode("pdf.result.hdr", pluginapi.UINodeRow, pluginapi.UIContainerProps{
				Justify: "between",
				Align:   "center",
			},
				liveNode("pdf.result.title", pluginapi.UINodeText, pluginapi.UITextProps{
					Text:       u.t("pdf.complete", "Completed"),
					Variant:    "on_field",
					FontSize:   12,
					FontWeight: 700,
					Grow:       true,
				}),
				liveNode("pdf.dismiss_banner", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Icon:     "close",
					IconOnly: true,
					Variant:  "ghost",
					Width:    24,
					Height:   24,
				}),
			),
			liveNode("pdf.result.txt", pluginapi.UINodeText, pluginapi.UITextProps{
				Text:    u.resultMsg,
				Variant: "on_field",
			}),
		}
		if u.resultAuthorized && len(u.resultOutputs) == 1 && u.resultOutputs[0] != "" {
			outPath := u.resultOutputs[0]
			children = append(children, liveNode("pdf.result.actions", pluginapi.UINodeRow, pluginapi.UIContainerProps{
				Gap:  6,
				Wrap: true,
			},
				liveNode("pdf.reveal_btn", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Label:       u.t("pdf.btn_reveal", "Reveal in Folder"),
					Variant:     "secondary",
					ShellAction: "reveal",
					ShellPath:   outPath,
					Height:      28,
				}),
				liveNode("pdf.open_btn", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Label:       u.t("pdf.btn_open", "Open PDF"),
					Variant:     "primary",
					ShellAction: "open",
					ShellPath:   outPath,
					Height:      28,
				}),
			))
		} else if len(u.resultOutputs) > 0 {
			children = append(children, liveNode("pdf.result.dir", pluginapi.UINodeText, pluginapi.UITextProps{
				Text:       filepath.Dir(u.resultOutputs[0]),
				Variant:    "on_field",
				FontSize:   11,
				SingleLine: true,
				Tooltip:    filepath.Dir(u.resultOutputs[0]),
			}))
		}
		n := liveNode("pdf.result", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
			Padding:     12,
			Gap:         8,
			Surface:     "field_support",
			BorderSize:  2,
			BorderToken: "accent",
		}, children...)
		return &n
	}
	return nil
}

func (u *LiveUI) buildModeControlsLocked() []pluginapi.UINode {
	switch u.activeTab {
	case "merge":
		nodes := []pluginapi.UINode{
			u.buildSectionHeaderLocked("pdf.merge.hdr", u.t("pdf.staged_files", "Staged Files"), len(u.mergeFiles), "pdf.merge.clear"),
			liveNode("pdf.merge.pick", pluginapi.UINodeFilePicker, pluginapi.UIFilePickerProps{
				Label:      u.t("pdf.btn_add_pdfs", "Add PDFs..."),
				Mode:       "open",
				Extensions: []string{"pdf"},
				Multiple:   true,
				Disabled:   u.running,
			}),
		}
		nodes = append(nodes, u.buildFileListLocked("pdf.merge.item", u.mergeFiles, true)...)
		nodes = append(nodes,
			liveNode("pdf.merge.outlbl", pluginapi.UINodeText, pluginapi.UITextProps{Text: u.t("pdf.output_name", "Output Filename"), Variant: "muted", FontSize: 12, FontWeight: 600}),
			liveNode("pdf.merge.outname", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{Value: u.mergeOutputName, Placeholder: "merged.pdf", Disabled: u.running}),
			liveNode("pdf.outdir", pluginapi.UINodeDirPicker, pluginapi.UIDirPickerProps{Label: u.t("pdf.save_dest", "Save Destination"), Value: u.outputDir, Disabled: u.running}),
		)
		return nodes

	case "split":
		picked := ""
		info := ""
		if u.splitFile != nil {
			picked = u.splitFile.Path
			if u.splitFile.PageCount > 0 {
				info = fmt.Sprintf("%s (%d pages)", u.splitFile.Name, u.splitFile.PageCount)
			} else {
				info = u.splitFile.Name
			}
		}
		nodes := []pluginapi.UINode{
			liveNode("pdf.split.pick", pluginapi.UINodeFilePicker, pluginapi.UIFilePickerProps{
				Label:      u.t("pdf.btn_select_pdf", "Choose PDF..."),
				Value:      picked,
				Mode:       "open",
				Extensions: []string{"pdf"},
				Disabled:   u.running,
			}),
		}
		if info != "" {
			nodes = append(nodes, liveNode("pdf.split.info", pluginapi.UINodeText, pluginapi.UITextProps{
				Text: info, Variant: "muted", FontSize: 12,
			}))
		}
		nodes = append(nodes, liveNode("pdf.split.mode", pluginapi.UINodeSelect, pluginapi.UISelectProps{
			Label:    u.t("pdf.split_method", "Split Method"),
			Value:    u.splitMode,
			Disabled: u.running,
			Options: []pluginapi.SurfaceOption{
				{Value: "all", Label: u.t("pdf.split_all", "Split by Page Span")},
				{Value: "range", Label: u.t("pdf.split_range", "Extract Page Range")},
			},
		}))
		if u.splitMode == "range" {
			nodes = append(nodes,
				liveNode("pdf.split.rangelbl", pluginapi.UINodeText, pluginapi.UITextProps{Text: u.t("pdf.page_range", "Page Selection (click pages or type e.g. 1-3, 5)"), Variant: "muted", FontSize: 12, FontWeight: 600}),
				liveNode("pdf.split.range", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{
					Value:       u.splitPageRange,
					Placeholder: "e.g. 1-3, 5, 8-10",
					Disabled:    u.running,
				}),
				liveNode("pdf.split.outlbl", pluginapi.UINodeText, pluginapi.UITextProps{Text: u.t("pdf.output_name", "Output Filename"), Variant: "muted", FontSize: 12, FontWeight: 600}),
				liveNode("pdf.split.outname", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{
					Value:       u.splitOutputName,
					Placeholder: "extracted.pdf",
					Disabled:    u.running,
				}),
			)
		} else {
			nodes = append(nodes,
				liveNode("pdf.split.spanlbl", pluginapi.UINodeText, pluginapi.UITextProps{Text: u.t("pdf.split_span", "Pages per File"), Variant: "muted", FontSize: 12, FontWeight: 600}),
				liveNode("pdf.split.span", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{
					Value:       u.splitSpan,
					Placeholder: "1",
					Disabled:    u.running,
				}),
			)
		}
		nodes = append(nodes, liveNode("pdf.outdir", pluginapi.UINodeDirPicker, pluginapi.UIDirPickerProps{
			Label: u.t("pdf.save_dest", "Save Destination"), Value: u.outputDir, Disabled: u.running,
		}))
		return nodes

	case "images":
		nodes := []pluginapi.UINode{
			u.buildSectionHeaderLocked("pdf.images.hdr", u.t("pdf.staged_files", "Staged Files"), len(u.imageFiles), "pdf.images.clear"),
			liveNode("pdf.images.pick", pluginapi.UINodeFilePicker, pluginapi.UIFilePickerProps{
				Label:      u.t("pdf.btn_add_images", "Add Images..."),
				Mode:       "open",
				Extensions: []string{"png", "jpg", "jpeg", "webp", "bmp"},
				Multiple:   true,
				Disabled:   u.running,
			}),
		}
		nodes = append(nodes, u.buildFileListLocked("pdf.images.item", u.imageFiles, true)...)
		nodes = append(nodes,
			liveNode("pdf.images.outlbl", pluginapi.UINodeText, pluginapi.UITextProps{Text: u.t("pdf.output_name", "Output Filename"), Variant: "muted", FontSize: 12, FontWeight: 600}),
			liveNode("pdf.images.outname", pluginapi.UINodeTextInput, pluginapi.UITextInputProps{Value: u.imageOutputName, Placeholder: "images.pdf", Disabled: u.running}),
			liveNode("pdf.outdir", pluginapi.UINodeDirPicker, pluginapi.UIDirPickerProps{Label: u.t("pdf.save_dest", "Save Destination"), Value: u.outputDir, Disabled: u.running}),
		)
		return nodes

	case "office":
		sofficeStatus := u.t("pdf.libreoffice_ok", "LibreOffice detected")
		statusVariant := "success"
		if _, err := pdfeng.FindSofficePath(u.sofficePath); err != nil {
			sofficeStatus = u.t("pdf.libreoffice_missing", "LibreOffice (soffice) not found. Configure its path in Settings.")
			statusVariant = "danger"
		}
		nodes := []pluginapi.UINode{
			liveNode("pdf.office.status", pluginapi.UINodeText, pluginapi.UITextProps{Text: sofficeStatus, Variant: statusVariant, FontSize: 12}),
			u.buildSectionHeaderLocked("pdf.office.hdr", u.t("pdf.staged_files", "Staged Files"), len(u.officeFiles), "pdf.office.clear"),
			liveNode("pdf.office.pick", pluginapi.UINodeFilePicker, pluginapi.UIFilePickerProps{
				Label:      u.t("pdf.btn_add_office", "Add Documents..."),
				Mode:       "open",
				Extensions: []string{"doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf"},
				Multiple:   true,
				Disabled:   u.running,
			}),
		}
		nodes = append(nodes, u.buildFileListLocked("pdf.office.item", u.officeFiles, false)...)
		nodes = append(nodes,
			liveNode("pdf.outdir", pluginapi.UINodeDirPicker, pluginapi.UIDirPickerProps{Label: u.t("pdf.save_dest", "Save Destination"), Value: u.outputDir, Disabled: u.running}),
		)
		return nodes
	}
	return nil
}

func (u *LiveUI) buildSectionHeaderLocked(id, title string, count int, clearID string) pluginapi.UINode {
	children := []pluginapi.UINode{
		liveNode(id+".title", pluginapi.UINodeText, pluginapi.UITextProps{
			Text:       fmt.Sprintf("%s (%d)", title, count),
			FontSize:   14,
			FontWeight: 700,
			Grow:       true,
		}),
	}
	if count > 0 {
		children = append(children, liveNode(clearID, pluginapi.UINodeButton, pluginapi.UIButtonProps{
			Label:    u.t("pdf.btn_clear", "Clear"),
			Variant:  "ghost",
			Height:   28,
			Disabled: u.running,
		}))
	}
	return liveNode(id, pluginapi.UINodeRow, pluginapi.UIContainerProps{
		Justify: "between",
		Align:   "center",
	}, children...)
}

func (u *LiveUI) buildFileListLocked(prefix string, items []liveFileItem, reorderable bool) []pluginapi.UINode {
	if len(items) == 0 {
		return nil
	}
	out := make([]pluginapi.UINode, 0, len(items))
	for i, it := range items {
		id := fmt.Sprintf("%s.%d", prefix, i)
		meta := it.SizeStr
		if it.PageCount > 0 {
			if meta != "" {
				meta = fmt.Sprintf("%s · %d pp", meta, it.PageCount)
			} else {
				meta = fmt.Sprintf("%d pp", it.PageCount)
			}
		}
		var rowChildren []pluginapi.UINode
		if it.AssetRef != "" {
			rowChildren = append(rowChildren, liveNode(id+".thumb", pluginapi.UINodeImage, pluginapi.UIImageProps{
				Src: it.AssetRef, Fit: "contain", Width: 32, Height: 32,
			}))
		} else {
			rowChildren = append(rowChildren, liveNode(id+".ic", pluginapi.UINodeIcon, pluginapi.UIIconProps{
				Name: "file-text", Size: 16, Tone: "muted",
			}))
		}
		rowChildren = append(rowChildren,
			liveNode(id+".jump", pluginapi.UINodeButton, pluginapi.UIButtonProps{
				Label:   it.Name,
				Variant: "ghost",
				Grow:    true,
				Tooltip: meta,
			}),
		)
		if reorderable {
			rowChildren = append(rowChildren,
				liveNode(id+".up", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Label: "↑", Variant: "secondary", Width: 28, Height: 28, PaddingX: 4, PaddingY: 2, Disabled: u.running || i == 0,
				}),
				liveNode(id+".down", pluginapi.UINodeButton, pluginapi.UIButtonProps{
					Label: "↓", Variant: "secondary", Width: 28, Height: 28, PaddingX: 4, PaddingY: 2, Disabled: u.running || i == len(items)-1,
				}),
			)
		}
		rowChildren = append(rowChildren,
			liveNode(id+".rm", pluginapi.UINodeButton, pluginapi.UIButtonProps{
				Icon: "trash", IconOnly: true, Variant: "danger", Width: 28, Height: 28, Disabled: u.running,
			}),
		)
		out = append(out, liveNode(id, pluginapi.UINodeRow, pluginapi.UIContainerProps{
			Padding: 6, Gap: 6, Align: "center", Surface: "ground", Border: true,
		}, rowChildren...))
	}
	return out
}

func (u *LiveUI) buildPageJumpLocked() pluginapi.UINode {
	total := len(u.previewPages)
	maxChips := total
	if maxChips > 24 {
		maxChips = 24
	}
	chips := make([]pluginapi.UINode, 0, maxChips)
	for p := 1; p <= maxChips; p++ {
		variant := "secondary"
		if p == u.currentPage {
			variant = "primary"
		}
		chips = append(chips, liveNode(fmt.Sprintf("rail_page_%d", p), pluginapi.UINodeButton, pluginapi.UIButtonProps{
			Label:    strconv.Itoa(p),
			Variant:  variant,
			Selected: p == u.currentPage,
			Height:   28,
			PaddingX: 8,
			PaddingY: 4,
		}))
	}
	return liveNode("pdf.page_jump", pluginapi.UINodeColumn, pluginapi.UIContainerProps{
		Gap: 8,
	},
		liveNode("pdf.page_jump.hdr", pluginapi.UINodeText, pluginapi.UITextProps{
			Text:       u.t("pdf.pages_quick_jump", "Pages"),
			FontSize:   13,
			FontWeight: 700,
		}),
		liveNode("pdf.page_jump.chips", pluginapi.UINodeRow, pluginapi.UIContainerProps{
			Gap:  6,
			Wrap: true,
		}, chips...),
	)
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

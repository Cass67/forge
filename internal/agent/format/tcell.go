package format

import "github.com/gdamore/tcell/v2"

func StyleToTcell(s Style) tcell.Style {
	base := tcell.StyleDefault
	switch s {
	case StyleDim:
		return base.Foreground(tcell.GetColor("#8b949e"))
	case StyleBold:
		return base.Bold(true).Foreground(tcell.GetColor("#f0f6fc"))
	case StyleToolBlue:
		return base.Foreground(tcell.GetColor("#58a6ff"))
	case StyleToolPurple:
		return base.Foreground(tcell.GetColor("#d2a8ff"))
	case StyleToolOrange:
		return base.Foreground(tcell.GetColor("#f0883e"))
	case StyleToolCyan:
		return base.Foreground(tcell.GetColor("#79c0ff"))
	case StyleDiffAdd:
		return base.Foreground(tcell.GetColor("#56d364")).Background(tcell.GetColor("#0f2d16"))
	case StyleDiffRemove:
		return base.Foreground(tcell.GetColor("#f85149")).Background(tcell.GetColor("#3d1117"))
	case StyleDiffHunk:
		return base.Foreground(tcell.GetColor("#8b949e"))
	case StyleAccentBorder:
		return base.Foreground(tcell.GetColor("#58a6ff"))
	case StyleSuccess:
		return base.Foreground(tcell.GetColor("#56d364"))
	case StyleError:
		return base.Foreground(tcell.GetColor("#f85149"))
	case StyleWarning:
		return base.Foreground(tcell.GetColor("#e3b341"))
	case StyleStats:
		return base.Foreground(tcell.GetColor("#8b949e"))
	default:
		return base.Foreground(tcell.GetColor("#f0f6fc"))
	}
}

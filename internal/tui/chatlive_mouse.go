package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

func (m *chatLiveModel) handleMouse(ev *tcell.EventMouse) {
	x, y := ev.Position()
	buttons := ev.Buttons()

	if buttons == tcell.ButtonNone {
		if m.panes.layout.scrollDrag.pane != "" {
			m.panes.layout.scrollDrag.pane = ""
			m.panes.layout.scrollDrag.offset = 0
		}
		if m.panes.layout.dividerDrag {
			m.panes.layout.dividerDrag = false
		}
		return
	}

	if m.overlays.helpVisible {
		if buttons&tcell.Button1 != 0 {
			m.overlays.helpVisible = false
		}
		return
	}

	if m.overlays.statsVisible {
		if buttons&tcell.Button1 != 0 {
			m.overlays.statsVisible = false
		}
		return
	}

	if m.overlays.files.visible {
		if buttons&tcell.Button1 != 0 {
			m.overlays.files.visible = false
		}
		return
	}

	if m.overlays.models.visible {
		m.handleModelPickerMouse(x, y, buttons)
		return
	}

	if m.overlays.sessions.visible {
		m.handleSessionsPickerMouse(x, y, buttons)
		return
	}

	ctx := m.mouseContext(x, y)

	if buttons&tcell.WheelUp != 0 {
		m.handlePaneWheel(ctx, -3)
		return
	}
	if buttons&tcell.WheelDown != 0 {
		m.handlePaneWheel(ctx, 3)
		return
	}

	if buttons&tcell.Button2 != 0 {
		m.handleMiddleClickExport(ctx)
		return
	}

	if buttons&tcell.Button1 != 0 {
		m.handleLeftClick(ctx, x, y)
	}
}

type chatMouseContext struct {
	leftX, leftY, leftW, leftH     int
	rightX, rightY, rightW, rightH int
	inputX, inputY, inputW, inputH int
	inputLineY                     int
	inLeft, inRight                bool
	inLeftScroll, inRightScroll    bool
	inDivider                      bool
}

func (m *chatLiveModel) mouseContext(x, y int) chatMouseContext {
	leftX, leftY, leftW, leftH := m.leftPaneRect()
	rightX, rightY, rightW, rightH := m.rightPaneRect()
	inputX, inputY, inputW, inputH := m.inputRect()
	leftScrollX := leftX + leftW - 2
	rightScrollX := rightX + rightW - 2
	dividerX := leftX + leftW
	inputLineY := inputY + inputH - 1
	if inputH >= 2 {
		inputLineY = inputY + 1
	}

	inLeft := x >= leftX && x < leftX+leftW && y >= leftY && y < leftY+leftH
	inRight := m.panes.layout.toolsVisible && x >= rightX && x < rightX+rightW && y >= rightY && y < rightY+rightH
	inLeftScroll := inLeft && x == leftScrollX && y > leftY && y < leftY+leftH-1
	inRightScroll := m.panes.layout.toolsVisible && inRight && x == rightScrollX && y > rightY && y < rightY+rightH-1
	inDivider := m.panes.layout.toolsVisible && x == dividerX && y >= leftY && y < leftY+leftH

	return chatMouseContext{
		leftX: leftX, leftY: leftY, leftW: leftW, leftH: leftH,
		rightX: rightX, rightY: rightY, rightW: rightW, rightH: rightH,
		inputX: inputX, inputY: inputY, inputW: inputW, inputH: inputH,
		inputLineY: inputLineY,
		inLeft:     inLeft, inRight: inRight,
		inLeftScroll: inLeftScroll, inRightScroll: inRightScroll,
		inDivider: inDivider,
	}
}

func (m *chatLiveModel) handleModelPickerMouse(x, y int, buttons tcell.ButtonMask) {
	if buttons&tcell.WheelUp != 0 {
		if m.overlays.models.cursor > 0 {
			m.overlays.models.cursor--
		}
		return
	}
	if buttons&tcell.WheelDown != 0 {
		if m.overlays.models.cursor < len(m.overlays.models.list)-1 {
			m.overlays.models.cursor++
		}
		return
	}
	if buttons&tcell.Button1 != 0 {
		x0, y0, maxW, boxH, visibleStart, visibleCount := m.modelPickerLayout()
		if x >= x0+1 && x < x0+maxW-1 && y >= y0+2 && y < y0+2+visibleCount && y < y0+boxH-1 {
			idx := visibleStart + (y - (y0 + 2))
			if idx >= 0 && idx < len(m.overlays.models.list) {
				m.overlays.models.cursor = idx
				m.pickModel(idx)
			}
			return
		}
		m.overlays.models.visible = false
	}
}

func (m *chatLiveModel) handleSessionsPickerMouse(x, y int, buttons tcell.ButtonMask) {
	if buttons&tcell.WheelUp != 0 {
		if m.overlays.sessions.cursor > 0 {
			m.overlays.sessions.cursor--
		}
		return
	}
	if buttons&tcell.WheelDown != 0 {
		if m.overlays.sessions.cursor < len(m.overlays.sessions.list)-1 {
			m.overlays.sessions.cursor++
		}
		return
	}
	if buttons&tcell.Button1 != 0 {
		x0, y0, maxW, boxH, visibleStart, visibleCount := m.sessionsPickerLayout()
		if x >= x0+1 && x < x0+maxW-1 && y >= y0+2 && y < y0+2+visibleCount && y < y0+boxH-1 {
			idx := visibleStart + (y - (y0 + 2))
			if idx >= 0 && idx < len(m.overlays.sessions.list) {
				m.overlays.sessions.cursor = idx
				m.restorePickedSession(idx)
			}
			return
		}
		m.overlays.sessions.visible = false
	}
}

func (m *chatLiveModel) handlePaneWheel(ctx chatMouseContext, delta int) {
	switch {
	case delta < 0 && ctx.inRight:
		m.panes.focusR = true
		m.panes.tools.scroll = clamp(m.panes.tools.scroll-3, 0, m.toolsMaxScroll())
		m.panes.tools.follow = m.panes.tools.scroll >= m.toolsMaxScroll()
	case delta < 0 && ctx.inLeft:
		m.panes.focusR = false
		m.panes.agent.scroll = clamp(m.panes.agent.scroll-3, 0, m.agentMaxScroll())
		m.panes.agent.follow = m.panes.agent.scroll >= m.agentMaxScroll()
	case delta > 0 && ctx.inRight:
		m.panes.focusR = true
		m.panes.tools.scroll = clamp(m.panes.tools.scroll+3, 0, m.toolsMaxScroll())
		m.panes.tools.follow = m.panes.tools.scroll >= m.toolsMaxScroll()
	case delta > 0 && ctx.inLeft:
		m.panes.focusR = false
		m.panes.agent.scroll = clamp(m.panes.agent.scroll+3, 0, m.agentMaxScroll())
		m.panes.agent.follow = m.panes.agent.scroll >= m.agentMaxScroll()
	default:
		m.scrollFocused(delta)
	}
}

func (m *chatLiveModel) handleMiddleClickExport(ctx chatMouseContext) {
	switch {
	case ctx.inLeft:
		m.panes.focusR = false
		content := m.selectedText("left")
		flash := "agent pane exported"
		if content == "" {
			content = m.panes.agent.buf
		} else {
			flash = "agent selection copied"
		}
		if err := m.copyBufferToFile("agent", content); err != nil {
			m.display.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.display.flash = flash
		}
	case ctx.inRight:
		m.panes.focusR = true
		content := m.selectedText("right")
		flash := "tools pane exported"
		if content == "" {
			content = m.panes.tools.buf
		} else {
			flash = "tools selection copied"
		}
		if err := m.copyBufferToFile("tools", content); err != nil {
			m.display.flash = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.display.flash = flash
		}
	}
}

func (m *chatLiveModel) handleLeftClick(ctx chatMouseContext, x, y int) {
	if m.panes.selectn.drag {
		switch {
		case ctx.inLeft:
			m.panes.focusR = false
			m.updateSelectionFromMouse("left", x, y)
			return
		case ctx.inRight:
			m.panes.focusR = true
			m.updateSelectionFromMouse("right", x, y)
			return
		default:
			m.panes.selectn.drag = false
		}
	}
	if m.panes.layout.dividerDrag {
		m.setLeftPaneWidth(x)
		return
	}
	if m.panes.layout.scrollDrag.pane != "" {
		m.handleScrollbarDrag(ctx, y)
		return
	}

	switch {
	case ctx.inLeftScroll:
		m.handleLeftScrollbarClick(ctx, y)
		return
	case ctx.inRightScroll:
		m.handleRightScrollbarClick(ctx, y)
		return
	case ctx.inDivider:
		m.panes.layout.dividerDrag = true
		m.setLeftPaneWidth(x)
		return
	case ctx.inLeft:
		m.panes.focusR = false
		m.beginSelectionFromMouse("left", x, y)
		return
	case ctx.inRight:
		m.panes.focusR = true
		m.beginSelectionFromMouse("right", x, y)
		return
	case !m.busy && m.approval == nil && y == ctx.inputLineY && x >= ctx.inputX+1 && x < ctx.inputX+ctx.inputW-1:
		m.inputPos = inputCursorFromScreenX(m.inputBuf, m.inputPos, x-(ctx.inputX+1), max(1, ctx.inputW-2))
		return
	}
}

func (m *chatLiveModel) handleScrollbarDrag(ctx chatMouseContext, y int) {
	switch m.panes.layout.scrollDrag.pane {
	case "left":
		m.panes.focusR = false
		m.panes.agent.scroll = scrollbarScrollForDrag(y, ctx.leftY, ctx.leftH, len(m.wrappedLines(&m.panes.agent, m.leftContentWidth())), m.agentVisibleHeight(), m.panes.layout.scrollDrag.offset)
		m.panes.agent.follow = m.panes.agent.scroll >= m.agentMaxScroll()
	case "right":
		m.panes.focusR = true
		m.panes.tools.scroll = scrollbarScrollForDrag(y, ctx.rightY, ctx.rightH, len(m.wrappedLines(&m.panes.tools, m.rightContentWidth())), m.toolsVisibleHeight(), m.panes.layout.scrollDrag.offset)
		m.panes.tools.follow = m.panes.tools.scroll >= m.toolsMaxScroll()
	}
}

func (m *chatLiveModel) handleLeftScrollbarClick(ctx chatMouseContext, y int) {
	m.panes.focusR = false
	total := len(m.wrappedLines(&m.panes.agent, m.leftContentWidth()))
	visible := m.agentVisibleHeight()
	thumbTop, thumbH := scrollbarThumb(ctx.leftY+2, max(1, visible-2), total, visible, m.panes.agent.scroll)
	switch {
	case y == ctx.leftY+1:
		m.panes.agent.scroll = clamp(m.panes.agent.scroll-1, 0, m.agentMaxScroll())
	case y == ctx.leftY+ctx.leftH-2:
		m.panes.agent.scroll = clamp(m.panes.agent.scroll+1, 0, m.agentMaxScroll())
	case y >= thumbTop && y < thumbTop+thumbH:
		m.panes.layout.scrollDrag.pane = "left"
		m.panes.layout.scrollDrag.offset = y - thumbTop
	case y < thumbTop:
		m.panes.agent.scroll = clamp(m.panes.agent.scroll-visible+1, 0, m.agentMaxScroll())
	default:
		m.panes.agent.scroll = clamp(m.panes.agent.scroll+visible-1, 0, m.agentMaxScroll())
	}
	m.panes.agent.follow = m.panes.agent.scroll >= m.agentMaxScroll()
}

func (m *chatLiveModel) handleRightScrollbarClick(ctx chatMouseContext, y int) {
	m.panes.focusR = true
	total := len(m.wrappedLines(&m.panes.tools, m.rightContentWidth()))
	visible := m.toolsVisibleHeight()
	thumbTop, thumbH := scrollbarThumb(ctx.rightY+2, max(1, visible-2), total, visible, m.panes.tools.scroll)
	switch {
	case y == ctx.rightY+1:
		m.panes.tools.scroll = clamp(m.panes.tools.scroll-1, 0, m.toolsMaxScroll())
	case y == ctx.rightY+ctx.rightH-2:
		m.panes.tools.scroll = clamp(m.panes.tools.scroll+1, 0, m.toolsMaxScroll())
	case y >= thumbTop && y < thumbTop+thumbH:
		m.panes.layout.scrollDrag.pane = "right"
		m.panes.layout.scrollDrag.offset = y - thumbTop
	case y < thumbTop:
		m.panes.tools.scroll = clamp(m.panes.tools.scroll-visible+1, 0, m.toolsMaxScroll())
	default:
		m.panes.tools.scroll = clamp(m.panes.tools.scroll+visible-1, 0, m.toolsMaxScroll())
	}
	m.panes.tools.follow = m.panes.tools.scroll >= m.toolsMaxScroll()
}

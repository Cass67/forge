# Forge TUI Redesign Spec

## Objective
Redesign the Forge TUI chat application to feel modern, airy, and welcoming, with an emphasis on excellent performance. The current design suffers from a "squashed together" layout and lacks clear prompt guidance in the input panel.

## Architecture & Layout

### Main Chat Area
- **Minimalist & Borderless**: Remove tight ASCII borders (`╭──╮`) around the chat pane.
- **Spacing**: Introduce generous vertical padding (e.g., `MarginBottom(1)`) between individual messages to create breathing room.
- **Visual Separation**: Rely on subtle visual indentations, colors, or padding to separate the user's messages from the AI's responses, rather than box borders.
- **Performance**: Retain Bubble Tea's `viewport` to ensure scrolling and rendering performance remains O(1) relative to chat history size.

### Input Area (Composer)
- **Borderless Dock**: Remove the heavy bounding box entirely.
- **Separation**: Use a single subtle top border or a distinct background color (e.g., a slightly lightened version of `AppBG`) to separate the input dock from the scrolling chat history.
- **Alignment**: Ensure the composer width aligns cleanly with the chat content for an airy feel.

### Empty State & Prompt Guidance
- **Input Placeholder**: When the user hasn't typed anything, a prominent but dim placeholder will be displayed directly inside the input area: `Ask Forge anything... (Press Enter to send)`.
- **Dynamic Behavior**: The placeholder will instantly disappear and be replaced by text as soon as the user begins typing.
- **Cursor and Prefix**: Replace the generic `> ` prefix with a cleaner visual treatment, such as a soft, colored vertical line on the left side or a simple indentation, to give a modern UI feel.
- **Status/Hint Text**: Hints (like `Alt+Enter for newline`) will be moved out of the heavy title bar and placed subtly below or to the right of the input area, keeping text entry focused and uncluttered.

## Implementation Details

### Component Updates (`internal/tui/chatcomposer.go`)
- Rewrite `ChatComposer.Render` to use the new lipgloss layout.
- Remove the `╭ Prompt ... ╮` logic and the hardcoded `> ` prefix.
- Inject the placeholder string dynamically when `c.text == ""`.
- Return styled text rows with a single top border (`─`) separating the chat area from the composer, instead of bordered wrappers.

### Styling Updates (`internal/tui/chattheme.go` & `internal/tui/chatmsg.go`)
- Adjust padding and margins (specifically adding `MarginBottom(1)` to chat bubbles).
- Introduce a subtle background color for the composer text entry box if necessary.
- Ensure the overall layout feels open by adjusting lipgloss paddings.
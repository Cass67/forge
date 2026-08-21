import { useEffect, useRef } from "react";
import { basicSetup } from "codemirror";
import {
  defaultHighlightStyle,
  LanguageDescription,
  syntaxHighlighting,
} from "@codemirror/language";
import { languages } from "@codemirror/language-data";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";

type Props = {
  path: string;
  value: string;
  onChange: (value: string) => void;
  onSave: () => void;
  onNotify: (message: string) => void;
};

export function CodeEditor({ path, value, onChange, onSave, onNotify }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const changeHandler = useRef(onChange);
  const saveHandler = useRef(onSave);

  changeHandler.current = onChange;
  saveHandler.current = onSave;

  useEffect(() => {
    if (!host.current) return;
    const language = new Compartment();
    const editor = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          basicSetup,
          EditorView.lineWrapping,
          syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
          language.of([]),
          keymap.of([
            {
              key: "Mod-s",
              preventDefault: true,
              run: () => {
                saveHandler.current();
                return true;
              },
            },
          ]),
          EditorView.updateListener.of((update) => {
            if (update.docChanged)
              changeHandler.current(update.state.doc.toString());
          }),
          EditorView.theme({
            "&": {
              height: "100%",
              backgroundColor: "var(--bg)",
              color: "var(--text)",
            },
            ".cm-scroller": {
              fontFamily: "var(--mono)",
              fontSize: "0.8125rem",
              lineHeight: "1.55",
            },
            ".cm-gutters": {
              backgroundColor: "var(--panel)",
              color: "var(--muted)",
              borderRight: "1px solid var(--border)",
            },
            ".cm-activeLine, .cm-activeLineGutter": {
              backgroundColor: "var(--hover)",
            },
            ".cm-content": { caretColor: "var(--text)" },
            ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--text)" },
            "&.cm-focused .cm-selectionBackground, .cm-selectionBackground": {
              backgroundColor: "var(--selected)",
            },
          }),
        ],
      }),
    });
    view.current = editor;

    let cancelled = false;
    const description = LanguageDescription.matchFilename(languages, path);
    if (description) {
      void description
        .load()
        .then((support) => {
          if (!cancelled)
            editor.dispatch({ effects: language.reconfigure(support) });
        })
        .catch((error: unknown) => {
          onNotify(
            `Could not load syntax highlighting for ${path}: ${String(error)}`,
          );
        });
    }
    return () => {
      cancelled = true;
      view.current = null;
      editor.destroy();
    };
  }, [onNotify, path]);

  useEffect(() => {
    const editor = view.current;
    if (!editor) return;
    const current = editor.state.doc.toString();
    if (current !== value) {
      editor.dispatch({
        changes: { from: 0, to: current.length, insert: value },
      });
    }
  }, [value]);

  return (
    <div
      className="workspace-code-editor"
      ref={host}
      aria-label={`Edit ${path}`}
    />
  );
}

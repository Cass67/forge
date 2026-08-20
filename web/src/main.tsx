import React from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./styles.css";

// A render throw otherwise unmounts the whole tree, and the desktop window has
// no console to read: the failure looks like a blank app. Show the stack
// instead, with inline styles so it survives a stylesheet that never loaded.
class ErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error("forge ui crashed", error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    return (
      <div
        style={{
          padding: "24px",
          font: "13px ui-monospace, SFMono-Regular, Menlo, monospace",
          color: "#e6e6e6",
          background: "#1c1c1c",
          height: "100vh",
          overflow: "auto",
        }}
      >
        <h1
          style={{ font: "600 15px system-ui, sans-serif", margin: "0 0 12px" }}
        >
          Forge UI crashed
        </h1>
        <pre style={{ whiteSpace: "pre-wrap", margin: "0 0 16px" }}>
          {error.stack ?? String(error)}
        </pre>
        <button
          onClick={() => this.setState({ error: null })}
          style={{
            font: "13px system-ui, sans-serif",
            padding: "6px 12px",
            color: "#e6e6e6",
            background: "#333",
            border: "1px solid #555",
            borderRadius: "6px",
            cursor: "pointer",
          }}
        >
          Retry render
        </button>
      </div>
    );
  }
}

const el = document.getElementById("root");
if (!el) throw new Error("root element missing");
createRoot(el).render(
  <React.StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
);

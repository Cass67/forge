import { useEffect, useState } from "react";
import { forge, type MCPServer } from "../bridge";

// MCPPanel shows every configured MCP server and what it actually
// contributed. "Configured but loaded nothing" is the interesting state — it
// means the server is enabled but failed to start or exposed no tools.
export function MCPPanel() {
  const [servers, setServers] = useState<MCPServer[] | null>(null);
  const [open, setOpen] = useState<Record<string, boolean>>({});

  useEffect(() => {
    void forge.mcpServers().then(setServers).catch(() => setServers([]));
  }, []);

  if (servers === null) return <div className="empty">checking…</div>;
  if (servers.length === 0) return <div className="empty">no MCP servers configured</div>;

  return (
    <>
      {servers.map((s) => {
        const state = !s.enabled ? "off" : s.loaded ? "on" : "warn";
        const label = !s.enabled ? "disabled" : s.loaded ? `${s.tools.length} tools` : "no tools loaded";
        return (
          <div className="mcp" key={s.name}>
            <button
              className="mcp-row"
              onClick={() => setOpen((o) => ({ ...o, [s.name]: !o[s.name] }))}
              title={s.target}
            >
              <span className={`mcp-dot ${state}`} />
              <span className="mcp-name">{s.name}</span>
              <span className="mcp-type">{s.type}</span>
              <span className={`mcp-state ${state}`}>{label}</span>
            </button>
            {open[s.name] ? (
              <div className="mcp-detail">
                {s.target ? <div className="mcp-target">{s.target}</div> : null}
                {s.tools.length > 0 ? (
                  <div className="mcp-tools">
                    {s.tools.map((t) => (
                      <span className="chip" key={t}>
                        {t}
                      </span>
                    ))}
                  </div>
                ) : (
                  <div className="empty">
                    {s.enabled ? "enabled, but contributed no tools this session" : "not enabled"}
                  </div>
                )}
              </div>
            ) : null}
          </div>
        );
      })}
    </>
  );
}

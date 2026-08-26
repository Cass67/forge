import { useState } from "react";
import { THEMES, type Theme } from "../theme";
import { SCALES, formatScale } from "../scale";
import { MAX_VIVIDNESS, vividnessLabel } from "../vividness";
import { forge, type InitPayload, type Provider } from "../bridge";
import { ProviderPanel } from "./ProviderPanel";
import { MCPPanel } from "./MCPPanel";
import { SkillsPanel } from "./SkillsPanel";
import type { DockLayoutPersistence } from "../dockLayout";

export type Prefs = {
  showTools: boolean;
  showReasoning: boolean;
  expandReasoning: boolean;
  expandTools: boolean;
  showActivity: boolean;
  showSidebar: boolean;
  scopeThreads: boolean;
  // Open a folder as a container: every repository under it becomes its own
  // workspace, rather than the folder itself being the one you work in.
  expandSubfolders: boolean;
  autoDiscoverSubfolders: boolean;
  dockLayoutPersistence: DockLayoutPersistence;
};

type SectionID =
  | "model"
  | "appearance"
  | "display"
  | "workspace"
  | "providers"
  | "mcp"
  | "skills";

const DISPLAY_TOGGLES: [keyof Omit<Prefs, "dockLayoutPersistence">, string][] =
  [
    ["showSidebar", "thread sidebar"],
    ["showActivity", "activity panel"],
    ["showTools", "tool cards"],
    ["showReasoning", "thinking blocks"],
    ["expandReasoning", "expand thinking by default"],
    ["expandTools", "expand tool and code panels by default"],
  ];

export function SettingsPanel({
  init,
  model,
  effort,
  theme,
  scale,
  vividness,
  prefs,
  onTheme,
  onScale,
  onVividness,
  onModel,
  onEffort,
  onPrefs,
  onProviders,
  onSkills,
  onAddWorkspace,
  onOpenWorkspaces,
  onNotify,
  onClose,
}: {
  init: InitPayload | null;
  model: string;
  effort: string;
  theme: Theme;
  scale: number;
  vividness: number;
  prefs: Prefs;
  onTheme: (t: Theme) => void;
  onScale: (s: number) => void;
  onVividness: (level: number) => void;
  onModel: () => void;
  onEffort: (e: string) => void;
  onPrefs: (p: Prefs) => void;
  onProviders: (next: Provider[]) => void;
  // Skills changed (installed/removed) — the refreshed list comes back so the
  // settings pane and the composer palette stay in sync.
  onSkills: (next: { name: string; description?: string }[]) => void;
  onAddWorkspace: () => void;
  onOpenWorkspaces: () => void;
  onNotify: (msg: string) => void;
  onClose: () => void;
}) {
  const [section, setSection] = useState<SectionID>("model");
  const efforts = init?.efforts ?? [];
  const providerCount = init?.providers.length ?? 0;
  const skillCount = init?.skills.length ?? 0;

  // Sections with nothing behind them yet are left out rather than opening to
  // an empty pane.
  const sections: { id: SectionID; label: string; count?: number }[] = [
    { id: "model", label: "model" },
    { id: "appearance", label: "appearance" },
    { id: "display", label: "display" },
    { id: "workspace", label: "workspace" },
    ...(providerCount > 0
      ? [{ id: "providers" as const, label: "providers", count: providerCount }]
      : []),
    { id: "mcp", label: "mcp servers" },
    { id: "skills", label: "skills", count: skillCount },
  ];
  const current = sections.some((s) => s.id === section) ? section : "model";

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal settings" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-badge">settings</span>
          <button className="icon-btn close" onClick={onClose}>
            ✕
          </button>
        </div>

        <div className="settings-body">
          <nav className="settings-nav">
            {sections.map((s) => (
              <button
                key={s.id}
                className={`settings-nav-item ${s.id === current ? "on" : ""}`}
                onClick={() => setSection(s.id)}
              >
                <span>{s.label}</span>
                {s.count ? (
                  <span className="settings-nav-count">{s.count}</span>
                ) : null}
              </button>
            ))}
          </nav>

          <div className="settings-pane">
            {current === "model" ? (
              <>
                <div className="set-row">
                  <span className="set-k">current</span>
                  <button className="btn" onClick={onModel}>
                    {model || "—"}
                  </button>
                </div>
                {efforts.length > 0 ? (
                  <div className="set-row">
                    <span className="set-k">effort</span>
                    <div className="seg">
                      {efforts.map((e) => (
                        <button
                          key={e}
                          className={`seg-btn ${e === effort ? "on" : ""}`}
                          onClick={() => onEffort(e)}
                        >
                          {e}
                        </button>
                      ))}
                    </div>
                  </div>
                ) : null}
                {init?.request_mode ? (
                  <div className="set-row">
                    <span className="set-k">mode</span>
                    <span className="set-v">{init.request_mode}</span>
                  </div>
                ) : null}
              </>
            ) : null}

            {current === "appearance" ? (
              <>
                <div className="set-row">
                  <span className="set-k">text size</span>
                  <div className="seg">
                    {SCALES.map((s) => (
                      <button
                        key={s}
                        className={`seg-btn ${Math.abs(s - scale) < 0.001 ? "on" : ""}`}
                        onClick={() => onScale(s)}
                        title={`Scale the whole interface to ${formatScale(s)}`}
                      >
                        {formatScale(s)}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="set-row">
                  <span className="set-k">brightness</span>
                  <input
                    aria-label="Theme brightness"
                    className="vivid-slider"
                    max={MAX_VIVIDNESS}
                    min={0}
                    onChange={(e) => onVividness(Number(e.target.value))}
                    step={1}
                    title="Lift the theme's text, surfaces and accents without changing its colours"
                    type="range"
                    value={vividness}
                  />
                  <span className="set-v muted-note">
                    {vividnessLabel(vividness)}
                  </span>
                </div>
                <div className="set-row">
                  <span className="set-k">theme</span>
                  <div className="seg wrap">
                    {THEMES.map((t) => (
                      <button
                        key={t}
                        className={`seg-btn ${t === theme ? "on" : ""}`}
                        onClick={() => onTheme(t)}
                      >
                        {t}
                      </button>
                    ))}
                  </div>
                </div>
              </>
            ) : null}

            {current === "display"
              ? DISPLAY_TOGGLES.map(([k, label]) => (
                  <label key={k} className="set-row toggle">
                    <input
                      type="checkbox"
                      checked={prefs[k]}
                      onChange={(e) =>
                        onPrefs({ ...prefs, [k]: e.target.checked })
                      }
                    />
                    <span>{label}</span>
                  </label>
                ))
              : null}

            {current === "workspace" ? (
              <>
                <div className="set-row">
                  <span className="set-k">current</span>
                  <span className="set-v mono">{init?.work_dir || "—"}</span>
                </div>
                <div className="set-row">
                  <span className="set-k">dock layout</span>
                  <div
                    aria-label="Remember dock layout"
                    className="seg wrap"
                    role="group"
                  >
                    {(
                      [
                        ["default", "Default"],
                        ["global", "Globally"],
                        ["workspace", "By workspace"],
                      ] as [DockLayoutPersistence, string][]
                    ).map(([value, label]) => (
                      <button
                        aria-pressed={prefs.dockLayoutPersistence === value}
                        className={`seg-btn ${prefs.dockLayoutPersistence === value ? "on" : ""}`}
                        key={value}
                        onClick={() =>
                          onPrefs({ ...prefs, dockLayoutPersistence: value })
                        }
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="set-row">
                  <span className="set-k" />
                  <span className="muted-note">
                    Default resets panel positions and sizes when a workspace
                    opens.
                  </span>
                </div>
                <label className="set-row toggle">
                  <input
                    type="checkbox"
                    checked={prefs.expandSubfolders}
                    onChange={(e) =>
                      onPrefs({ ...prefs, expandSubfolders: e.target.checked })
                    }
                  />
                  <span>
                    opening a folder adds each subfolder as its own workspace
                  </span>
                </label>
                <label className="set-row toggle">
                  <input
                    type="checkbox"
                    checked={prefs.autoDiscoverSubfolders}
                    onChange={(e) =>
                      onPrefs({
                        ...prefs,
                        autoDiscoverSubfolders: e.target.checked,
                      })
                    }
                  />
                  <span>
                    automatically add new subfolders from opened containers
                  </span>
                </label>
                <div className="set-row">
                  <span className="set-k" />
                  <button className="btn" onClick={onAddWorkspace}>
                    Open folder…
                  </button>
                  <button className="btn" onClick={onOpenWorkspaces}>
                    Recent workspaces
                  </button>
                </div>
              </>
            ) : null}

            {current === "providers" && init ? (
              <ProviderPanel
                providers={init.providers}
                onChange={onProviders}
                onNotify={onNotify}
              />
            ) : null}

            {current === "mcp" ? <MCPPanel /> : null}

            {current === "skills" && init ? (
              <SkillsPanel
                skills={init.skills}
                onSkills={onSkills}
                onNotify={onNotify}
              />
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}

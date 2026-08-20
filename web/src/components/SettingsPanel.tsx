import { THEMES, type Theme } from "../theme";
import type { InitPayload, Provider } from "../bridge";
import { ProviderPanel } from "./ProviderPanel";

export type Prefs = {
  showTools: boolean;
  showReasoning: boolean;
  showActivity: boolean;
  showSidebar: boolean;
  scopeThreads: boolean;
};

export function SettingsPanel({
  init,
  model,
  effort,
  theme,
  prefs,
  onTheme,
  onModel,
  onEffort,
  onPrefs,
  onProviders,
  onNotify,
  onClose,
}: {
  init: InitPayload | null;
  model: string;
  effort: string;
  theme: Theme;
  prefs: Prefs;
  onTheme: (t: Theme) => void;
  onModel: () => void;
  onEffort: (e: string) => void;
  onPrefs: (p: Prefs) => void;
  onProviders: (next: Provider[]) => void;
  onNotify: (msg: string) => void;
  onClose: () => void;
}) {
  const efforts = init?.efforts ?? [];
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal settings" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-badge">settings</span>
          <button className="icon-btn close" onClick={onClose}>
            ✕
          </button>
        </div>

        <div className="set-section">model</div>
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
                <button key={e} className={`seg-btn ${e === effort ? "on" : ""}`} onClick={() => onEffort(e)}>
                  {e}
                </button>
              ))}
            </div>
          </div>
        ) : null}

        <div className="set-section">theme</div>
        <div className="set-row">
          <div className="seg wrap">
            {THEMES.map((t) => (
              <button key={t} className={`seg-btn ${t === theme ? "on" : ""}`} onClick={() => onTheme(t)}>
                {t}
              </button>
            ))}
          </div>
        </div>

        <div className="set-section">display</div>
        {(
          [
            ["showSidebar", "thread sidebar"],
            ["showActivity", "activity panel"],
            ["showTools", "tool cards"],
            ["showReasoning", "thinking blocks"],
            ["scopeThreads", "only this workspace's threads"],
          ] as [keyof Prefs, string][]
        ).map(([k, label]) => (
          <label key={k} className="set-row toggle">
            <input type="checkbox" checked={prefs[k]} onChange={(e) => onPrefs({ ...prefs, [k]: e.target.checked })} />
            <span>{label}</span>
          </label>
        ))}

        <div className="set-section">workspace</div>
        <div className="set-row">
          <span className="set-k">dir</span>
          <span className="set-v mono">{init?.work_dir || "—"}</span>
        </div>
        {init?.request_mode ? (
          <div className="set-row">
            <span className="set-k">mode</span>
            <span className="set-v">{init.request_mode}</span>
          </div>
        ) : null}
        {init && init.providers.length > 0 ? (
          <>
            <div className="set-section">providers</div>
            <ProviderPanel providers={init.providers} onChange={onProviders} onNotify={onNotify} />
          </>
        ) : null}
        {init && init.skills.length > 0 ? (
          <>
            <div className="set-section">skills ({init.skills.length})</div>
            <div className="skill-list">
              {init.skills.map((s) => (
                <div className="skill" key={s.name}>
                  <span className="skill-name">/{s.name}</span>
                  <span className="skill-desc">{s.description}</span>
                </div>
              ))}
            </div>
          </>
        ) : null}
      </div>
    </div>
  );
}

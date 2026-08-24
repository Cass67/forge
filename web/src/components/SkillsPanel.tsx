import { useState } from "react";
import { forge } from "../bridge";

type Skill = { name: string; description?: string };

export function SkillsPanel({
  skills,
  onSkills,
  onNotify,
}: {
  skills: Skill[];
  onSkills: (next: Skill[]) => void;
  onNotify: (msg: string) => void;
}) {
  // "project" installs into this workspace's .forge/skills; "global" into the
  // user's config dir so the skill is available everywhere.
  const [scope, setScope] = useState<"project" | "global">("project");
  const [source, setSource] = useState("");
  const [busy, setBusy] = useState(false);

  async function install() {
    const src = source.trim();
    if (!src) return;
    setBusy(true);
    try {
      const next = await forge.installSkill(src, scope);
      onSkills(next);
      setSource("");
      onNotify(`installed skill; /help and / will list it`);
    } catch (e: unknown) {
      onNotify(String(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove(name: string) {
    setBusy(true);
    try {
      const next = await forge.removeSkill(name);
      onSkills(next);
      onNotify(`removed /${name}`);
    } catch (e: unknown) {
      onNotify(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="skill-panel">
      <div className="skill-install">
        <div className="skill-scope" role="group" aria-label="install scope">
          <button
            className={scope === "project" ? "scope on" : "scope"}
            onClick={() => setScope("project")}
          >
            this workspace
          </button>
          <button
            className={scope === "global" ? "scope on" : "scope"}
            onClick={() => setScope("global")}
          >
            all workspaces
          </button>
        </div>
        <input
          className="skill-source"
          value={source}
          placeholder="URL to SKILL.md, owner/repo:subdir, or a local path"
          onChange={(e) => setSource(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !busy) void install();
          }}
          aria-label="skill source"
        />
        <button
          className="skill-add"
          onClick={() => void install()}
          disabled={busy || !source.trim()}
        >
          {busy ? "installing…" : "add skill"}
        </button>
      </div>
      <div className="skill-hint">
        git: <span className="mono">owner/repo</span> or a repo URL · raw: a URL
        ending in .md · local: a file or folder on disk
      </div>
      {skills.length === 0 ? (
        <div className="empty">no skills installed</div>
      ) : (
        <div className="skill-list">
          {skills.map((s) => (
            <div className="skill" key={s.name}>
              <span className="skill-name">/{s.name}</span>
              <span className="skill-desc">{s.description}</span>
              <button
                className="skill-remove"
                onClick={() => void remove(s.name)}
                disabled={busy}
              >
                remove
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

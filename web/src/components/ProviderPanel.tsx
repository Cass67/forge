import { useState } from "react";
import { forge, type Login, type Provider } from "../bridge";

type Busy = { id: string; note: string } | null;

// ProviderPanel manages credentials: sign out, paste an API key, or run a
// browser sign-in. Device-code flows show the URL and code and then wait;
// Claude returns a URL and needs the callback pasted back.
export function ProviderPanel({
  providers,
  onChange,
  onNotify,
}: {
  providers: Provider[];
  onChange: (next: Provider[]) => void;
  onNotify: (msg: string) => void;
}) {
  const [busy, setBusy] = useState<Busy>(null);
  const [login, setLogin] = useState<Login | null>(null);
  const [keyFor, setKeyFor] = useState<string>("");
  const [keyValue, setKeyValue] = useState("");

  const fail = (e: unknown) => {
    setBusy(null);
    onNotify(String(e));
  };

  function signOut(p: Provider) {
    setBusy({ id: p.id, note: "signing out…" });
    forge
      .signOutProvider(p.id)
      .then((next) => {
        onChange(next);
        setBusy(null);
        onNotify(`signed out of ${p.label}`);
      })
      .catch(fail);
  }

  function startLogin(p: Provider) {
    setBusy({ id: p.id, note: "starting sign-in…" });
    forge
      .startProviderLogin(p.id)
      .then((l) => {
        setLogin(l);
        if (l.needs_paste) {
          setBusy(null);
          return;
        }
        setBusy({ id: p.id, note: "waiting for authorization…" });
        return forge.awaitProviderLogin(p.id).then((next) => {
          onChange(next);
          setBusy(null);
          setLogin(null);
          onNotify(`signed in to ${p.label}`);
        });
      })
      .catch(fail);
  }

  function saveKey(p: Provider) {
    const key = keyValue.trim();
    if (!key) return;
    setBusy({ id: p.id, note: "saving…" });
    setKeyValue("");
    setKeyFor("");
    forge
      .setProviderKey(p.id, key)
      .then((next) => {
        onChange(next);
        setBusy(null);
        onNotify(`saved ${p.label} key`);
      })
      .catch(fail);
  }

  function completeLogin() {
    if (!login) return;
    const pasted = keyValue.trim();
    if (!pasted) return;
    setKeyValue("");
    setBusy({ id: login.provider, note: "completing…" });
    forge
      .completeProviderLogin(login.provider, pasted)
      .then((next) => {
        onChange(next);
        setBusy(null);
        setLogin(null);
        onNotify("signed in");
      })
      .catch(fail);
  }

  return (
    <>
      {providers.map((p) => {
        const working = busy?.id === p.id;
        const pasting = login?.needs_paste && login.provider === p.id;
        return (
          <div className="prov" key={p.id}>
            <div className="prov-row">
              <span className={`prov-dot ${p.signed_in ? "on" : ""}`} />
              <span className="prov-name">{p.label || p.id}</span>
              <span className="prov-status">{working ? busy.note : p.status || (p.signed_in ? "signed in" : "not signed in")}</span>
              {p.signed_in ? (
                <button className="btn" disabled={working} onClick={() => signOut(p)}>
                  Sign out
                </button>
              ) : p.interactive ? (
                <button className="btn" disabled={working} onClick={() => startLogin(p)}>
                  Sign in
                </button>
              ) : (
                <button
                  className="btn"
                  disabled={working}
                  onClick={() => {
                    setKeyFor(keyFor === p.id ? "" : p.id);
                    setKeyValue("");
                  }}
                >
                  {keyFor === p.id ? "Cancel" : "Add key"}
                </button>
              )}
            </div>

            {keyFor === p.id ? (
              <div className="prov-input">
                <input
                  type="password"
                  className="picker-search"
                  autoFocus
                  placeholder={`${p.label} API key`}
                  value={keyValue}
                  onChange={(e) => setKeyValue(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && saveKey(p)}
                />
                <button className="btn primary" onClick={() => saveKey(p)}>
                  Save
                </button>
              </div>
            ) : null}

            {login && login.provider === p.id ? (
              <div className="prov-login">
                <div className="prov-login-row">
                  <span>Open</span>
                  <button className="linkish" onClick={() => void forge.openURL(login.verify_url)}>
                    {login.verify_url}
                  </button>
                </div>
                {login.user_code ? (
                  <div className="prov-login-row">
                    <span>Code</span>
                    <code className="prov-code">{login.user_code}</code>
                  </div>
                ) : null}
                {pasting ? (
                  <div className="prov-input">
                    <input
                      className="picker-search"
                      autoFocus
                      placeholder="paste the callback URL or code"
                      value={keyValue}
                      onChange={(e) => setKeyValue(e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && completeLogin()}
                    />
                    <button className="btn primary" onClick={completeLogin}>
                      Finish
                    </button>
                  </div>
                ) : null}
              </div>
            ) : null}
          </div>
        );
      })}
    </>
  );
}

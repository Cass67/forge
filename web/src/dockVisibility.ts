export function initialDockVisibility(showDocks: boolean): boolean {
  return showDocks;
}

export function initialYoloState(yolo: boolean): boolean {
  return yolo;
}

// A runtime starts with its own default. The first time a new session appears,
// apply the saved setting; returning to an existing live session preserves its
// per-session toggle.
export function sessionYoloState(
  seen: boolean,
  saved: boolean,
  runtime: boolean,
): boolean {
  return seen ? runtime : saved;
}

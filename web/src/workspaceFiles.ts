export type OpenFile = {
  path: string;
  content: string;
  savedContent: string;
  version: string;
  // Scratch files live outside the workspace in the throwaway scratch dir, so
  // they load via readScratchFile and save via writeScratchFile rather than
  // their workspace counterparts. path is the bare file name for these.
  scratch?: boolean;
};

export function isDirty(file: OpenFile): boolean {
  return file.content !== file.savedContent;
}

export function acceptSavedFile(
  file: OpenFile,
  saved: Pick<OpenFile, "content" | "version">,
  expectedVersion: string,
): OpenFile {
  if (file.version !== expectedVersion) return file;
  return { ...file, savedContent: saved.content, version: saved.version };
}

export function fuzzyScore(path: string, query: string): number | null {
  const haystack = path.toLowerCase();
  const needle = query.trim().toLowerCase();
  if (!needle) return 0;
  let position = 0;
  let score = 0;
  let previous = -2;
  for (const char of needle) {
    const found = haystack.indexOf(char, position);
    if (found < 0) return null;
    score += found === previous + 1 ? 4 : 1;
    if (found === 0 || "/_-".includes(haystack[found - 1] ?? "")) score += 3;
    score -= found * 0.01;
    previous = found;
    position = found + 1;
  }
  return score - path.length * 0.001;
}

export function filterPaths(
  paths: string[],
  query: string,
  limit = 100,
): string[] {
  return paths
    .map((path) => ({ path, score: fuzzyScore(path, query) }))
    .filter(
      (result): result is { path: string; score: number } =>
        result.score !== null,
    )
    .sort((a, b) => b.score - a.score || a.path.localeCompare(b.path))
    .slice(0, limit)
    .map(({ path }) => path);
}

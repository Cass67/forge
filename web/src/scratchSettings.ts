export async function saveScratchDirectory(
  input: string,
  save: (dir: string) => Promise<string>,
  apply: (dir: string) => void,
): Promise<string> {
  const dir = input.trim();
  const resolved = await save(dir);
  apply(resolved);
  return resolved;
}

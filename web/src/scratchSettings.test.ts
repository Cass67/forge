import { expect, test } from "bun:test";
import { saveScratchDirectory } from "./scratchSettings";

test("saving scratch directory applies backend-resolved path to UI state", async () => {
  let shown = "/old/scratch";
  const resolved = await saveScratchDirectory(
    " ~/scratch ",
    async (dir) => {
      expect(dir).toBe("~/scratch");
      return "/resolved/scratch";
    },
    (dir) => {
      shown = dir;
    },
  );

  expect(resolved).toBe("/resolved/scratch");
  expect(shown).toBe("/resolved/scratch");
});

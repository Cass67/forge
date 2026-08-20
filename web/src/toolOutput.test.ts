import { expect, test } from "bun:test";
import { parseGutter, languageForPath } from "./toolOutput";

test("parses read_file gutter", () => {
  const parsed = parseGutter(
    "   1 | package main\n   2 | \n   3 | func main() {}\n",
  );
  expect(parsed).not.toBeNull();
  expect(parsed!.numbers).toEqual([1, 2, 3]);
  expect(parsed!.code).toBe("package main\n\nfunc main() {}");
});

test("keeps the File status header out of the code", () => {
  const parsed = parseGutter(
    "File status: a.go (modified)\n  10 | x := 1\n  11 | y := 2\n",
  );
  expect(parsed!.header).toBe("File status: a.go (modified)");
  expect(parsed!.code).toBe("x := 1\ny := 2");
});

test("leaves command output alone", () => {
  expect(
    parseGutter(
      "commit 87d2d61\nAuthor: Cass\n\n    feat(gui): add workspace\n",
    ),
  ).toBeNull();
  expect(parseGutter("ok  \tforge/internal/lsp\t0.4s\n")).toBeNull();
});

test("picks language from the path, or nothing", () => {
  expect(languageForPath("web/src/bridge.ts")).toBe("typescript");
  expect(languageForPath("internal/lsp/lsp.go")).toBe("go");
  expect(languageForPath("some/file.unknownext")).toBe("");
  expect(languageForPath("no path here")).toBe("");
});

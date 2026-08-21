import { expect, test } from "bun:test";
import {
  changeAnchors,
  diffStat,
  parseDiff,
  splitRows,
  wordSpans,
} from "./diff";

const sample = `diff --git a/src/a.ts b/src/a.ts
index 1111111..2222222 100644
--- a/src/a.ts
+++ b/src/a.ts
@@ -1,4 +1,4 @@
 const keep = 1;
-const value = compute(oldName);
+const value = compute(newName);
 const after = 2;
@@ -20,2 +20,3 @@
 tail();
+extra();
diff --git a/src/new.ts b/src/new.ts
--- /dev/null
+++ b/src/new.ts
@@ -0,0 +1,2 @@
+first
+second
`;

test("parses files, hunks and line numbers", () => {
  const files = parseDiff(sample);
  expect(files.length).toBe(2);
  expect(files[0].path).toBe("src/a.ts");
  expect(files[0].hunks.length).toBe(2);
  expect(files[0].adds).toBe(2);
  expect(files[0].dels).toBe(1);

  const rows = files[0].hunks[0].rows;
  expect(rows.map((r) => r.kind)).toEqual(["ctx", "del", "add", "ctx"]);
  expect(rows[0].old).toBe(1);
  expect(rows[0].neu).toBe(1);
  // The line after a replacement keeps both sides counting independently.
  expect(rows[3].old).toBe(3);
  expect(rows[3].neu).toBe(3);
  expect(files[0].hunks[1].newStart).toBe(20);
});

test("recognises an added file", () => {
  const files = parseDiff(sample);
  expect(files[1].path).toBe("src/new.ts");
  expect(files[1].added).toBe(true);
  expect(files[1].adds).toBe(2);
});

test("marks only the changed word inside a replaced line", () => {
  const rows = parseDiff(sample)[0].hunks[0].rows;
  const del = rows[1];
  const add = rows[2];
  expect(del.spans?.length).toBe(1);
  expect(del.text.slice(...(del.spans as [number, number][])[0])).toBe(
    "oldName",
  );
  expect(add.text.slice(...(add.spans as [number, number][])[0])).toBe(
    "newName",
  );
});

test("wordSpans gives up when two lines share nothing", () => {
  const [before, after] = wordSpans("alpha", "beta");
  expect(before).toEqual([]);
  expect(after).toEqual([]);
});

test("splitRows pairs a replacement and pads an uneven block", () => {
  const rows = parseDiff(sample)[0].hunks[0].rows;
  const split = splitRows(rows);
  expect(split.length).toBe(3);
  expect(split[1].left?.kind).toBe("del");
  expect(split[1].right?.kind).toBe("add");

  const lone = parseDiff(sample)[0].hunks[1].rows;
  const loneSplit = splitRows(lone);
  expect(loneSplit[1].left).toBe(null);
  expect(loneSplit[1].right?.kind).toBe("add");
});

test("changeAnchors marks one anchor per changed block", () => {
  const files = parseDiff(sample);
  // a.ts: one replacement, one addition. new.ts: one block of two adds.
  expect(changeAnchors(files).length).toBe(3);
});

test("diffStat totals every file", () => {
  expect(diffStat(parseDiff(sample))).toEqual({ adds: 4, dels: 1 });
});

test("a headerless fragment still renders", () => {
  const files = parseDiff("@@ -1 +1 @@\n-a\n+b\n");
  expect(files.length).toBe(1);
  expect(files[0].hunks[0].rows.length).toBe(2);
});

test("paths containing ' b/' survive the git header", () => {
  const files = parseDiff(
    "diff --git a/lib b/lib.ts b/lib b/lib.ts\n--- a/lib b/lib.ts\n+++ b/lib b/lib.ts\n@@ -1 +1 @@\n-x\n+y\n",
  );
  expect(files[0].path).toBe("lib b/lib.ts");
});

test("changeAnchors stamps the first row of every changed block", () => {
  const files = parseDiff(sample);
  const anchors = changeAnchors(files);
  const rows = files[0].hunks[0].rows;
  // The deletion opens the block; the addition that follows continues it.
  expect(rows[1].anchor).toBe(anchors[0]);
  expect(rows[2].anchor).toBeUndefined();
  expect(rows[0].anchor).toBeUndefined();
});

test("a pure deletion is still reachable in side-by-side view", () => {
  const files = parseDiff(
    "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,1 @@\n a\n-gone\n",
  );
  changeAnchors(files);
  const split = splitRows(files[0].hunks[0].rows);
  // The deleted line has no facing addition, so its anchor must sit on the
  // left cell or next-change would skip it entirely.
  expect(split[1].right).toBe(null);
  expect(split[1].left?.anchor).toBeTruthy();
});

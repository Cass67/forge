import { expect, test } from "bun:test";
import {
  formatPick,
  MAX_PREVIEW_LOGS,
  type PreviewLog,
  type PreviewPick,
  pushLog,
} from "./previewContext";

const pick: PreviewPick = {
  selector: "body > main > button:nth-of-type(2)",
  tag: "button",
  id: "save",
  classes: "primary wide",
  text: "Save",
  html: '<button id="save" class="primary wide">Save</button>',
  box: { x: 410, y: 220, width: 120, height: 32 },
  styles: { display: "flex", color: "rgb(255, 255, 255)" },
  url: "http://localhost:5173/settings",
  viewport: { width: 1280, height: 800 },
  screenshot: "data:image/png;base64,AAA",
};

test("a picked element reads as a bug report the agent can act on", () => {
  const logs: PreviewLog[] = [
    { level: "error", text: "TypeError: save is not a function", at: 1 },
  ];
  const message = formatPick(pick, logs, "this button does nothing");

  expect(message).toContain("this button does nothing");
  expect(message).toContain("button#save.primary.wide");
  expect(message).toContain("body > main > button:nth-of-type(2)");
  expect(message).toContain("120×32 at (410, 220)");
  expect(message).toContain("display: flex");
  expect(message).toContain("[error] TypeError: save is not a function");
  expect(message).toContain("screenshot of the page is attached");
});

test("a failed screenshot says so instead of going quiet", () => {
  const message = formatPick(
    { ...pick, screenshot: "", screenshotError: "tainted canvas" },
    [],
    "",
  );
  expect(message).toContain("No screenshot: tainted canvas");
  expect(message).not.toContain("Console");
});

test("the console buffer keeps the newest lines and stays bounded", () => {
  let logs: PreviewLog[] = [];
  for (let index = 0; index < MAX_PREVIEW_LOGS + 20; index++) {
    logs = pushLog(logs, { level: "warn", text: `line ${index}`, at: index });
  }
  expect(logs).toHaveLength(MAX_PREVIEW_LOGS);
  expect(logs[logs.length - 1].text).toBe(`line ${MAX_PREVIEW_LOGS + 19}`);
});

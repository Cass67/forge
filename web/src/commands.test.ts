import { expect, test } from "bun:test";
import { matchCommands } from "./commands";

const pluginCommands = [{ name: "/sandbox", description: "Docker sandbox" }];

test("typing an argument closes the palette so Enter submits the line as typed", () => {
  // "/sandbox on" must not resolve back to the bare "/sandbox" entry: picking
  // it would drop the argument and run the wrong subcommand.
  expect(matchCommands("/sandbox on", [], pluginCommands)).toEqual([]);
});

test("name prefix still completes while no argument is typed", () => {
  const items = matchCommands("/sand", [], pluginCommands);
  expect(items.map((c) => c.name)).toContain("/sandbox");
});

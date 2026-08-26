/// <reference path="./bun-test.d.ts" />

import { describe, expect, test } from "bun:test";
import { decideModelPickerOpen } from "./modelPicker";

describe("decideModelPickerOpen", () => {
  test("keeps picker closed and clears stale models without a signed-in provider", () => {
    expect(
      decideModelPickerOpen([
        {
          id: "example",
          label: "Example",
          signed_in: false,
          interactive: true,
        },
      ]),
    ).toEqual({
      open: false,
      models: [],
      notice: "Sign in to a provider in Settings before choosing a model.",
    });
  });
});

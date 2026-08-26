import type { Provider } from "./bridge";

export type ModelPickerOpenDecision =
  | { open: true }
  | { open: false; models: []; notice: string };

export function decideModelPickerOpen(
  providers: Provider[],
): ModelPickerOpenDecision {
  if (providers.some((provider) => provider.signed_in)) return { open: true };
  return {
    open: false,
    models: [],
    notice: "Sign in to a provider in Settings before choosing a model.",
  };
}

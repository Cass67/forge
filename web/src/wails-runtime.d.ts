// The Wails runtime is served by the window at a fixed path, not resolved from
// node_modules, so it is declared here and mapped in tsconfig "paths".
export declare const Call: {
  ByName(name: string, ...args: unknown[]): Promise<unknown>;
};
export declare const Events: {
  On(name: string, callback: (event: unknown) => void): () => void;
  Emit(name: string, data?: unknown): void;
};

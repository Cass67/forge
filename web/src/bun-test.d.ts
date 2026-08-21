declare module "bun:test" {
  export const expect: (value: unknown) => any;
  export const test: (name: string, run: () => void | Promise<void>) => void;
}

// Load the actual stylesheet: Vitest's default CSS stub does not expose ?raw.
export function readAppStyles(): string {
  const process = (
    globalThis as typeof globalThis & {
      process: {
        cwd(): string;
        getBuiltinModule(name: "node:fs"): {
          readFileSync(path: string, encoding: "utf8"): string;
        };
      };
    }
  ).process;
  return process
    .getBuiltinModule("node:fs")
    .readFileSync(`${process.cwd()}/src/styles.css`, "utf8");
}

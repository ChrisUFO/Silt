import { createRequire } from "node:module";
import { resolve, dirname } from "node:path";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

// sharp is a frontend devDependency. Bare ESM specifiers resolve relative to
// this file (scripts/), not the caller's cwd, and Node ESM ignores NODE_PATH —
// so resolve sharp through the frontend package to keep this working from any
// build script (build.sh on Git Bash and build-linux.sh).
const __dirname = dirname(fileURLToPath(import.meta.url));
const require = createRequire(resolve(__dirname, "../frontend/package.json"));
const sharp = require("sharp");

const args = process.argv.slice(2);
if (args.length < 2) {
  console.error("Usage: node generate-icon.mjs <input.svg> <output.png>");
  process.exit(1);
}

const [inputSvg, outputPng] = args.map((p) => resolve(p));

mkdirSync(dirname(outputPng), { recursive: true });

await sharp(inputSvg)
  .resize(1024, 1024, { fit: "contain", background: { r: 0, g: 0, b: 0, alpha: 0 } })
  .png()
  .toFile(outputPng);

console.log(`Icon written: ${outputPng}`);

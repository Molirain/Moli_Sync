import fs from "node:fs";
import path from "node:path";

const root = process.cwd();
const out = path.join(root, "dist");
const entries = ["index.html", "privacy.html", "assets", "docs", "_headers", "robots.txt"];

fs.rmSync(out, { recursive: true, force: true });
fs.mkdirSync(out, { recursive: true });

for (const entry of entries) {
  const from = path.join(root, entry);
  if (!fs.existsSync(from)) throw new Error(`Missing build input: ${from}`);
  fs.cpSync(from, path.join(out, entry), { recursive: true });
}

console.log(`staged ${entries.length} entries in dist/`);

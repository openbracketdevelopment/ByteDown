// Node.js 20+; no npm packages. Run on a trusted server, never in a browser.
import { readFile } from "node:fs/promises";
import { basename } from "node:path";

const key = process.env.API_KEY;
if (!key || Buffer.byteLength(key) < 32) throw new Error("API_KEY must be at least 32 bytes");
const base = new URL(process.env.BASE_URL || "http://127.0.0.1:8080");
const filename = process.argv[2];
if (!filename) throw new Error("Usage: node examples/upload.mjs path/to/photo.jpg");
async function request(path, options = {}) {
  const url = new URL(path, base);
  if (url.origin !== base.origin) throw new Error("Refusing to send credentials off-origin");
  const response = await fetch(url, {
    ...options,
    headers: { ...options.headers, Authorization: ["Bearer", key].join(" ") },
    redirect: "error",
    signal: options.signal ?? AbortSignal.timeout(30_000),
  });
  if (!response.ok) throw new Error(`ByteDown HTTP ${response.status}`);
  return response.json();
}
const form = new FormData();
form.set("file", new Blob([await readFile(filename)]), basename(filename));
form.set("profile", "web");
const upload = await request("/v1/images", { method: "POST", body: form });
const deadline = Date.now() + 120_000;
let job;
do {
  const remaining = deadline - Date.now();
  if (remaining <= 0) throw new Error("Processing timed out");
  job = await request(upload.status_url, {
    signal: AbortSignal.timeout(Math.min(30_000, remaining)),
  });
  if (job.status === "failed") throw new Error("Image processing failed");
  if (job.status === "succeeded") break;
  if (!["queued", "processing"].includes(job.status)) throw new Error("Unknown job state");
  if (Date.now() >= deadline) throw new Error("Processing timed out");
  await new Promise((resolve) => setTimeout(resolve, 1000));
} while (true);
const manifest = await request(`/v1/images/${encodeURIComponent(upload.image_id)}`);
console.log(JSON.stringify({ job, manifest }, null, 2));

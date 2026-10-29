// Contract-only checks; this fake HTTP server does not encode images.
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";

const directory = `examples/.test-results-${process.pid}`;
const png = Buffer.from(await readFile("examples/fixture.png.base64", "utf8"), "base64");
const key = "x".repeat(32);
let corrupt = false;
let uploads = 0;
const server = createServer(async (request, response) => {
  if (request.headers.authorization !== ["Bearer", key].join(" ")) {
    response.writeHead(401).end();
    return;
  }
  const json = (body) => {
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify(body));
  };
  if (request.url === "/v1/images" && request.method === "POST") {
    const body = [];
    for await (const chunk of request) body.push(chunk);
    assert.match(Buffer.concat(body).toString(), /name="profile"/);
    assert.match(Buffer.concat(body).toString(), /web/);
    uploads++;
    json({ image_id: "image", job_id: "job", status_url: "/v1/jobs/job" });
  } else if (request.url === "/v1/jobs/job") {
    json({ status: "succeeded", generated: 1, planned: 1, duration_ms: 12 });
  } else if (request.url === "/v1/images/image") {
    json({
      image_id: "image",
      original: { format: "png", width: 64, height: 48, bytes: png.length },
      original_url: "/v1/images/image/original",
      outputs: [{
        id: "variant", format: "png", width: 64, height: 48,
        bytes: png.length + (corrupt ? 1 : 0), url: "/media/variant",
        smaller: false, size_difference_percent: 0, includes_resizing: false,
      }],
    });
  } else if (["/v1/images/image/original", "/media/variant"].includes(request.url)) {
    response.setHeader("Content-Type", "image/png");
    response.end(png);
  } else {
    response.writeHead(404).end();
  }
});
function run(command, args, environment) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { env: environment, stdio: ["ignore", "pipe", "pipe"] });
    let output = "";
    child.stdout.on("data", (chunk) => { output += chunk; });
    child.stderr.on("data", (chunk) => { output += chunk; });
    child.once("error", reject);
    child.once("close", (code) => resolve({ code, output }));
  });
}
try {
  await mkdir(directory, { recursive: true, mode: 0o700 });
  await writeFile(`${directory}/input.png`, png);
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const environment = {
    ...process.env, API_KEY: key,
    BASE_URL: `http://127.0.0.1:${server.address().port}`,
    RESULTS_DIR: `${directory}/results`,
  };
  const demo = await run("bash", ["examples/demo.sh"], environment);
  assert.equal(demo.code, 0, demo.output);
  const upload = await run(process.execPath, ["examples/upload.mjs", `${directory}/input.png`], environment);
  assert.equal(upload.code, 0, upload.output);
  assert.equal(JSON.parse(upload.output).job.duration_ms, 12);
  const benchmark = await run("bash", ["examples/benchmark.sh", `${directory}/input.png`], {
    ...environment, RESULTS_DIR: `${directory}/benchmark`,
  });
  assert.equal(benchmark.code, 0, benchmark.output);
  assert.match(await readFile(`${directory}/benchmark/summary.txt`, "utf8"), /observed_wall_seconds=/);
  corrupt = true;
  const invalid = await run("bash", ["examples/demo.sh"], environment);
  assert.notEqual(invalid.code, 0, invalid.output);
  assert.match(invalid.output, /Byte count mismatch/);
  assert.equal(uploads, 4);
  console.log("Example contract checks passed (demo, upload, benchmark, corrupt-byte rejection).");
} finally {
  await new Promise((resolve) => server.close(resolve));
  await rm(directory, { recursive: true, force: true });
}

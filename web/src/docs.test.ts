// @vitest-environment jsdom
import { afterEach, beforeEach, it, expect, vi } from "vitest";
import { readFileSync } from "node:fs";
const html = readFileSync("../docs/reference.html", "utf8");
const script = readFileSync("../docs/reference.js", "utf8");
beforeEach(() => {
  document.body.innerHTML = html;
  new Function(script)();
});
afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});
it("switches examples with pointer and keyboard controls", () => {
  const node = document.getElementById("tab-node")!;
  node.click();
  expect(node.getAttribute("aria-selected")).toBe("true");
  expect(document.getElementById("example-node")!.hidden).toBe(false);
  expect(document.getElementById("example-curl")!.hidden).toBe(true);
  node.dispatchEvent(
    new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
  );
  expect(document.activeElement?.id).toBe("tab-python");
  document.activeElement?.dispatchEvent(
    new KeyboardEvent("keydown", { key: "Home", bubbles: true }),
  );
  expect(document.activeElement?.id).toBe("tab-curl");
});
it("copies example text without credentials or markup", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  (document.querySelector(".copy") as HTMLButtonElement).click();
  await Promise.resolve();
  expect(writeText).toHaveBeenCalledWith(
    expect.stringContaining("$BYTEDOWN_API_KEY"),
  );
  expect(document.getElementById("copy-status")!.textContent).toContain(
    "copied",
  );
});
it("has working internal navigation and a local OpenAPI contract", () => {
  for (const link of document.querySelectorAll('a[href^="#"]'))
    expect(document.querySelector(link.getAttribute("href")!)).not.toBeNull();
  expect(document.querySelector('a[href="/openapi.yaml"]')).not.toBeNull();
  expect(document.querySelector('script[src^="https:"]')).toBeNull();
});

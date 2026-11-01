// @vitest-environment jsdom
import {
  render,
  screen,
  waitFor,
  cleanup,
  fireEvent,
} from "@testing-library/svelte";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, it, expect, vi } from "vitest";
import App from "./App.svelte";
const id = "a".repeat(32),
  job = "b".repeat(32);
const output = {
  id: "c".repeat(32),
  format: "png",
  width: 100,
  height: 80,
  bytes: 108,
  url: "/media/output",
  size_difference_percent: -8,
  includes_resizing: false,
};
const image = {
  id,
  job_id: job,
  status: "succeeded",
  original: { format: "png", width: 100, height: 80, bytes: 100 },
  original_url: `/v1/images/${id}/original`,
  outputs: [output],
};
let status = "succeeded",
  expired = false,
  fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  sessionStorage.clear();
  status = "succeeded";
  expired = false;
  fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (expired) return new Response("expired", { status: 401 });
    if (init?.method === "HEAD") return new Response(null, { status: 200 });
    if (path === "/ui/session" || path === "/ui/login")
      return Response.json({ csrf: "test-csrf" });
    if (path === "/ui/logout") return new Response(null, { status: 204 });
    if (path === "/v1/capabilities")
      return Response.json({
        profiles: { web: { include_source: true, formats: ["webp", "avif"] } },
        limits: { upload_bytes: 200, pixels: 40000000, dimension: 20000 },
      });
    if (path.startsWith("/v1/jobs/"))
      return Response.json({
        status,
        generated: status === "queued" ? 0 : 1,
        planned: 3,
        error_message: "Codec failed",
      });
    if (init?.method === "DELETE") return new Response(null, { status: 204 });
    if (path.startsWith("/v1/images/")) return Response.json(image);
    throw Error("Unexpected request " + path);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function restore() {
  sessionStorage.setItem(
    "bytedown-jobs",
    JSON.stringify([
      {
        key: "key",
        name: "transparent.png",
        size: 100,
        id,
        job,
        profile: "web",
      },
    ]),
  );
}
it("shows mixed input feedback while retaining valid JPEG and PNG files", async () => {
  render(App);
  await screen.findByText("Choose images");
  const input = document.getElementById("images") as HTMLInputElement;
  await fireEvent.change(input, {
    target: {
      files: [
        new File(["jpg"], "photo.jpg", { type: "image/jpeg" }),
        new File(["png"], "alpha.png", { type: "image/png" }),
        new File(["gif"], "bad.gif"),
        new File([new Uint8Array(201)], "big.png"),
      ],
    },
  });
  expect(screen.getByText("photo.jpg")).toBeTruthy();
  expect(screen.getByText("alpha.png")).toBeTruthy();
  expect(screen.getByText("Choose a JPG or PNG image.")).toBeTruthy();
  expect(screen.getByText(/Exceeds/)).toBeTruthy();
  expect(
    (screen.getByText(/Optimize pending/) as HTMLButtonElement).disabled,
  ).toBe(false);
});
it("restores completion, reports larger files and missing formats, and downloads a protected output", async () => {
  restore();
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});
  render(App);
  await screen.findByText("8.0% larger");
  expect(screen.getAllByText("Unavailable")).toHaveLength(2);
  expect(screen.getByText("100 × 80")).toBeTruthy();
  await fireEvent.click(screen.getByText("Download ↓"));
  await waitFor(() => expect(click).toHaveBeenCalled());
  expect(document.querySelector(".thumbnail")?.getAttribute("src")).toBe(
    "/media/output",
  );
});
it.each(["queued", "processing"])(
  "restores %s output counts without invented percentages",
  async (state) => {
    status = state;
    restore();
    render(App);
    await waitFor(() =>
      expect(document.querySelector(".status")?.textContent).toContain(state),
    );
    if (state === "processing")
      expect(document.querySelector(".status")?.textContent).toContain(
        "1/3 outputs",
      );
  },
);
it("shows processing failure distinctly", async () => {
  status = "failed";
  restore();
  render(App);
  await screen.findByText("Processing failed: Codec failed");
});
it("requires confirmation and sends CSRF on deletion", async () => {
  restore();
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(App);
  await screen.findByText("8.0% larger");
  await fireEvent.click(screen.getByText("Delete from server"));
  await waitFor(() =>
    expect(fetcher).toHaveBeenCalledWith(
      `/v1/images/${id}`,
      expect.objectContaining({
        method: "DELETE",
        headers: expect.objectContaining({ "X-CSRF-Token": "test-csrf" }),
      }),
    ),
  );
  expect(window.confirm).toHaveBeenCalled();
});
it("loads transparent previews on demand and keeps downloads if decoding fails", async () => {
  restore();
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute("open", "");
  };
  render(App);
  await screen.findByText("8.0% larger");
  expect(screen.queryByRole("dialog")).toBeNull();
  await fireEvent.click(screen.getByText("Preview"));
  expect(screen.getByRole("dialog")).toBeTruthy();
  expect(document.querySelectorAll(".checker")).toHaveLength(2);
  await fireEvent.error(screen.getByAltText("PNG generated output"));
  expect(screen.getByText(/Preview unavailable/)).toBeTruthy();
});
it("supports keyboard file selection and optimize button navigation", async () => {
  render(App);
  await screen.findByText("Choose images");
  const user = userEvent.setup();
  const input = document.getElementById("images") as HTMLInputElement;
  input.focus();
  expect(document.activeElement).toBe(input);
  await user.upload(
    input,
    new File(["image"], "keyboard.png", { type: "image/png" }),
  );
  expect(screen.getByText("keyboard.png")).toBeTruthy();
  await user.tab();
  expect(document.activeElement?.tagName).toBe("BUTTON");
});
it("handles authentication expiration without retaining credentials", async () => {
  fetcher.mockImplementationOnce(
    async () => new Response("expired", { status: 401 }),
  );
  render(App);
  await screen.findByLabelText("Operator password");
  expect(sessionStorage.getItem("password")).toBeNull();
});
it("keeps an interrupted upload retry on the same idempotency key", async () => {
  const instances: FakeXHR[] = [];
  class FakeXHR {
    status = 202;
    responseText = JSON.stringify({ image_id: id, job_id: job });
    upload = { onprogress: null };
    headers: Record<string, string> = {};
    onerror = () => {};
    onload = () => {};
    onloadend = () => {};
    onabort = () => {};
    ontimeout = () => {};
    open() {}
    setRequestHeader(k: string, v: string) {
      this.headers[k] = v;
    }
    send() {
      instances.push(this);
      queueMicrotask(() => {
        if (instances.length === 1) this.onerror();
        else this.onload();
      });
    }
    abort() {
      this.onabort();
    }
  }
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
  render(App);
  await screen.findByText("Choose images");
  await fireEvent.change(document.getElementById("images")!, {
    target: { files: [new File(["image"], "retry.png")] },
  });
  await fireEvent.click(screen.getByText(/Optimize pending/));
  await screen.findByText(/Connection interrupted/);
  await fireEvent.click(screen.getByText("Retry same upload"));
  await waitFor(() => expect(instances).toHaveLength(2));
  expect(instances[0].headers["Idempotency-Key"]).toBe(
    instances[1].headers["Idempotency-Key"],
  );
});

it("reports expiration before starting a protected download", async () => {
  restore();
  render(App);
  await screen.findByText("8.0% larger");
  expired = true;
  await fireEvent.click(screen.getByText("Download ↓"));
  await screen.findByLabelText("Operator password");
  expect(screen.getByText(/Your session expired/)).toBeTruthy();
});
it("reports a file deleted before download", async () => {
  restore();
  render(App);
  await screen.findByText("8.0% larger");
  fetcher.mockImplementationOnce(
    async () => new Response(null, { status: 404 }),
  );
  await fireEvent.click(screen.getByText("Download ↓"));
  await screen.findByText(/This file or result is no longer available/);
});

it("retries a manifest interrupted after processing completed", async () => {
  restore();
  const original = fetcher.getMockImplementation()! as (path: string, init?: RequestInit) => Promise<Response>;
  let interrupted = false;
  fetcher.mockImplementation(async (path: string, init?: RequestInit) => {
    if (path === `/v1/images/${id}` && !interrupted) {
      interrupted = true;
      throw Error("Network interrupted");
    }
    return original(path, init);
  });
  render(App);
  await screen.findByText(/Unable to retrieve result/);
  const now = Date.now();
  vi.spyOn(Date, "now").mockReturnValue(now + 10000);
  await waitFor(() => expect(screen.getByText("8.0% larger")).toBeTruthy(), {
    timeout: 3500,
  });
  expect(screen.queryByText(/Unable to retrieve result/)).toBeNull();
});

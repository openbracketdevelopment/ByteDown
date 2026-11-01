<script lang="ts" context="module">
  function openDialog(node: HTMLDialogElement) {
    const previous = document.activeElement as HTMLElement | null;
    node.showModal();
    return {
      destroy() {
        previous?.focus();
      },
    };
  }
</script>

<script lang="ts">
  import { onMount } from "svelte";
  import {
    bytes,
    difference,
    primary,
    invalid,
    type ImageResult,
    type Output,
  } from "./lib";
  type Item = {
    key: string;
    name: string;
    size: number;
    file?: File;
    profile: string;
    state: string;
    progress: number;
    id?: string;
    job?: string;
    error?: string;
    image?: ImageResult;
    generated?: number;
    planned?: number;
    deleted?: boolean;
    failures: number;
    next: number;
  };
  let csrf = "",
    signed = false,
    password = "",
    message = "",
    items: Item[] = [],
    busy = false,
    loading = true;
  let profiles: Record<string, { include_source: boolean; formats: string[] }> =
      {},
    profile = "web",
    limit = 20971520,
    pixels = 40000000,
    dimension = 20000;
  let input: HTMLInputElement;
  const uploads = new Set<XMLHttpRequest>();
  const requests = new Set<AbortController>();
  let stopped = false;
  let polling = false;
  let preview: { original: string; output: string; label: string } | null =
    null;
  let previewError = false;
  const batchLimit = Math.max(
    1,
    Math.min(10, Number(import.meta.env.VITE_BATCH_LIMIT) || 10),
  );
  const uploadConcurrency = Math.max(
    1,
    Math.min(2, Number(import.meta.env.VITE_UPLOAD_CONCURRENCY) || 2),
  );
  function refresh() {
    items = [...items];
    save();
  }
  function save() {
    sessionStorage.setItem(
      "bytedown-jobs",
      JSON.stringify(
        items
          .filter((i) => (i.id || i.job) && i.state !== "deleted")
          .map(({ key, name, size, profile, state, id, job }) => ({
            key,
            name,
            size,
            profile,
            state,
            id,
            job,
          })),
      ),
    );
  }
  async function request(path: string, init: RequestInit = {}) {
    const controller = new AbortController();
    requests.add(controller);
    const timeout = setTimeout(() => controller.abort(), 15000);
    let res: Response;
    try {
      res = await fetch(path, {
        ...init,
        signal: controller.signal,
        headers: { ...init.headers, "X-CSRF-Token": csrf },
      });
    } finally {
      clearTimeout(timeout);
      requests.delete(controller);
    }
    if (res.status === 404)
      throw Error("This file or result is no longer available.");
    if (res.status === 401) {
      signed = false;
      throw Error("Your session expired. Sign in to continue.");
    }
    if (!res.ok) {
      const text = await res.text();
      try {
        throw Error(JSON.parse(text).error.message);
      } catch (e) {
        if (e instanceof SyntaxError)
          throw Error(text || `Request failed (${res.status})`);
        throw e;
      }
    }
    return res;
  }
  async function setup() {
    const cap = await (await request("/v1/capabilities")).json();
    profiles = cap.profiles;
    limit = cap.limits.upload_bytes;
    pixels = cap.limits.pixels;
    dimension = cap.limits.dimension;
    profile =
      Object.keys(profiles).find(
        (p) =>
          profiles[p].include_source &&
          profiles[p].formats.includes("avif") &&
          profiles[p].formats.includes("webp"),
      ) || Object.keys(profiles)[0];
    signed = true;
    try {
      const recent = JSON.parse(
        sessionStorage.getItem("bytedown-jobs") || "[]",
      );
      if (Array.isArray(recent) && !items.length)
        items = recent
          .slice(0, 10)
          .filter(
            (i) => typeof i.id === "string" && /^[a-f0-9]{32}$/.test(i.id),
          )
          .map((i) => ({
            ...i,
            state: "restoring",
            failures: 0,
            next: 0,
            progress: 0,
          }));
    } catch {
      message = "Recent jobs could not be restored.";
    }
    await poll();
  }
  async function login() {
    message = "";
    try {
      const res = await fetch("/ui/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password }),
      });
      password = "";
      if (!res.ok) throw Error(await res.text());
      csrf = (await res.json()).csrf;
      await setup();
    } catch (e) {
      message = String(e);
    }
  }
  async function logout() {
    try {
      await request("/ui/logout", { method: "POST" });
      signed = false;
      csrf = "";
      closePreview();
    } catch (e) {
      message = String(e);
    }
  }
  function select(files: FileList | File[] | null) {
    if (!files) return;
    const remaining = batchLimit - items.length;
    const all = Array.from(files);
    if (all.length > remaining)
      message = `Only ${Math.max(0, remaining)} more files fit in this batch. Clear finished items to make room.`;
    for (const file of all.slice(0, Math.max(0, remaining))) {
      const error = invalid(file, limit);
      items.push({
        key: crypto.randomUUID(),
        name: file.name,
        size: file.size,
        file,
        profile,
        state: error ? "failed" : "selected",
        progress: 0,
        error,
        failures: 0,
        next: 0,
      });
    }
    refresh();
    if (input) input.value = "";
  }
  async function upload(item: Item) {
    while (uploads.size >= uploadConcurrency && !stopped)
      await new Promise((resolve) => setTimeout(resolve, 100));
    if (stopped) return;
    return new Promise<void>((resolve) => {
      if (!item.file) {
        resolve();
        return;
      }
      item.state = "uploading";
      item.error = "";
      refresh();
      const xhr = new XMLHttpRequest();
      uploads.add(xhr);
      xhr.onloadend = () => uploads.delete(xhr);
      xhr.onabort = () => resolve();
      xhr.open("POST", "/v1/images");
      xhr.timeout = 180000;
      xhr.setRequestHeader("X-CSRF-Token", csrf);
      xhr.setRequestHeader("Idempotency-Key", item.key);
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) {
          item.progress = Math.round((e.loaded / e.total) * 100);
          refresh();
        }
      };
      xhr.onload = () => {
        try {
          if (xhr.status === 401) {
            signed = false;
            throw Error("Session expired. Sign in, then retry this upload.");
          }
          const data = JSON.parse(xhr.responseText);
          if (xhr.status !== 202 && xhr.status !== 200)
            throw Error(data.error?.message || "Upload failed");
          item.id = data.image_id;
          item.job = data.job_id;
          item.state = "queued";
          item.file = undefined;
          item.next = 0;
        } catch (e) {
          item.state = "upload-failed";
          item.error = String(e);
        }
        refresh();
        resolve();
      };
      xhr.onerror = xhr.ontimeout = () => {
        item.state = "upload-failed";
        item.error =
          "Connection interrupted. Retry reuses the same submission key; the server may already have accepted it.";
        refresh();
        resolve();
      };
      const data = new FormData();
      data.append("file", item.file);
      data.append("profile", item.profile);
      xhr.send(data);
    });
  }
  async function optimize() {
    busy = true;
    const pending = items.filter((i) => i.state === "selected");
    let cursor = 0;
    await Promise.all(
      Array.from({ length: uploadConcurrency }).map(async () => {
        while (cursor < pending.length && signed) {
          const item = pending[cursor++];
          await upload(item);
        }
      }),
    );
    busy = false;
    await poll();
  }
  async function poll() {
    if (polling || !signed || stopped) return;
    polling = true;
    try {
      for (const item of items.filter(
        (i) =>
          i.id &&
          !["completed", "failed", "deleted"].includes(i.state) &&
          i.next <= Date.now(),
      )) {
        if (stopped || !signed) break;
        try {
          if (item.job) {
            const job = await (await request(`/v1/jobs/${item.job}`)).json();
            if (item.deleted) continue;
            item.state = job.status === "succeeded" ? "processing" : job.status;
            item.generated = job.generated;
            item.planned = job.planned;
            if (job.status === "failed")
              item.error = `Processing failed: ${job.error_message || job.error_code}`;
            if (job.status === "succeeded") {
              const image = await (
                await request(`/v1/images/${item.id}`)
              ).json();
              if (item.deleted) continue;
              item.image = image;
              item.state = "completed";
            }
          } else {
            const image = await (await request(`/v1/images/${item.id}`)).json();
            if (item.deleted) continue;
            item.image = image;
            item.job = item.image!.job_id;
            item.state =
              item.image!.status === "succeeded"
                ? "completed"
                : item.image!.status;
          }
          if (item.state !== "failed") item.error = "";
          item.failures = 0;
          item.next = Date.now() + 2000;
        } catch (e) {
          if (item.deleted) continue;
          item.failures++;
          item.error = `Unable to retrieve result (it may have been deleted): ${String(e)}`;
          item.next = Date.now() + Math.min(30000, 2000 * 2 ** item.failures);
          if (item.failures >= 5) item.state = "failed";
        }
        refresh();
      }
    } finally {
      polling = false;
    }
  }
  function clearFinished() {
    items = items.filter(
      (i) => !["completed", "failed", "deleted"].includes(i.state),
    );
    save();
  }
  async function remove(item: Item) {
    if (
      !confirm(
        `Delete ${item.name} and all its stored files from the server? This cannot be undone.`,
      )
    )
      return;
    try {
      await request(`/v1/images/${item.id}`, { method: "DELETE" });
      item.deleted = true;
      item.state = "deleted";
      item.image = undefined;
      refresh();
      closePreview();
    } catch (e) {
      message = String(e);
    }
  }
  async function download(url: string, name: string) {
    try {
      // Check access/existence before the browser streams the actual download.
      await request(url, { method: "HEAD" });
      const a = document.createElement("a");
      a.href = url;
      a.download = name;
      document.body.append(a);
      a.click();
      a.remove();
    } catch (e) {
      message = String(e);
    }
  }
  function archive(selected: Item[]) {
    const ids = selected
      .filter((i) => i.state === "completed")
      .map((i) => i.id);
    if (ids.length)
      download(`/v1/archive?images=${ids.join(",")}`, "bytedown-results.zip");
  }
  function filename(item: Item, o: Output) {
    return `${item.name.replace(/\.[^.]+$/, "")}-${o.includes_resizing ? `${o.width}w` : "optimized"}.${o.format === "jpeg" ? "jpg" : o.format}`;
  }
  function closePreview() {
    preview = null;
    previewError = false;
  }
  onMount(() => {
    stopped = false;
    (async () => {
      try {
        const res = await fetch("/ui/session");
        if (res.ok) {
          csrf = (await res.json()).csrf;
          await setup();
        }
      } catch (e) {
        message = String(e);
      } finally {
        loading = false;
      }
    })();
    const timer = setInterval(() => void poll(), 2000);
    return () => {
      stopped = true;
      clearInterval(timer);
      for (const xhr of uploads) xhr.abort();
      for (const request of requests) request.abort();
    };
  });
</script>

<svelte:head><title>ByteDown · Image workshop</title></svelte:head>
<header>
  <a class="brand" href="/"
    ><img src="/logo.svg" alt="" width="42" height="42" />ByteDown<span
      class="brand-dot">.</span
    ></a
  >
  <nav>
    <a href="/api-docs" target="_blank" rel="noreferrer">API documentation ↗</a
    >{#if signed}<button class="quiet" onclick={logout}>Sign out</button>{/if}
  </nav>
</header>
<main>
  <div class="eyebrow"><span></span> LESS WEIGHT. SAME POSSIBILITIES.</div>
  <section class="intro">
    <div>
      <h1>Smaller images.<br /><em>Ready for the web.</em></h1>
      <p>
        Upload JPG or PNG images. Download optimized originals,<br
          class="desktop"
        /> WebP, and AVIF versions.
      </p>
    </div>
    <div class="format-art" aria-hidden="true">
      <div class="tile back">PNG <span>↘</span></div>
      <div class="tile front">JPG <span>↓</span></div>
      <div class="art-caption">A little lighter. A lot more ready.</div>
    </div>
  </section>
  {#if message}<div class="notice" role="alert">
      {message}<button
        class="quiet"
        onclick={() => (message = "")}
        aria-label="Dismiss notice">×</button
      >
    </div>{/if}
  {#if loading}<p role="status">Opening your workshop…</p>{:else if !signed}
    <section class="login panel">
      <div>
        <span class="eyebrow">YOUR PRIVATE WORKSHOP</span>
        <h2>Let’s lighten things up.</h2>
        <p>
          Sign in with your operator password to upload and manage images on
          this instance.
        </p>
      </div>
      <form
        onsubmit={(e) => {
          e.preventDefault();
          void login();
        }}
      >
        <label for="password">Operator password</label><input
          id="password"
          type="password"
          bind:value={password}
          autocomplete="current-password"
          required
        /><button class="primary">Sign in <span>↗</span></button>
      </form>
    </section>
  {:else}
    <section
      class="dropzone"
      ondragover={(e) => e.preventDefault()}
      ondrop={(e) => {
        e.preventDefault();
        select(e.dataTransfer?.files || null);
      }}
      aria-label="Image upload area"
    >
      <div class="upload-icon" aria-hidden="true">↥</div>
      <h2>Good images. Smaller footprint.</h2>
      <p>Drop your images here, or choose them below.</p>
      <label class="primary file-button" for="images"
        >Choose images <span>＋</span></label
      ><input
        class="file-input"
        bind:this={input}
        id="images"
        type="file"
        accept="image/jpeg,image/png,.jpg,.jpeg,.png"
        multiple
        onchange={() => select(input.files)}
      /><small
        >JPG & PNG · up to {bytes(limit)} each · {batchLimit} files per batch</small
      ><small
        >Up to {(pixels / 1000000).toFixed(0)} megapixels · {dimension.toLocaleString()}
        px per dimension</small
      >
    </section>
    <div class="workflow">
      <span><b>01</b> Add your images</span><span
        ><b>02</b> Let us do the light lifting</span
      ><span><b>03</b> Download & go</span>
    </div>
    <section class="workspace">
      <div class="section-heading">
        <div>
          <span class="eyebrow">THE WORKBENCH</span>
          <h2>Your images <span class="count">{items.length}</span></h2>
        </div>
        <div class="actions">
          {#if Object.keys(profiles).length > 1}<label
              >Profile <select bind:value={profile}
                >{#each Object.keys(profiles) as name}<option value={name}
                    >{name}</option
                  >{/each}</select
              ></label
            >{/if}<button class="quiet" onclick={clearFinished}
            >Clear finished from view</button
          ><button
            disabled={!items.some((i) => i.state === "completed")}
            onclick={() => archive(items)}>Download completed ZIP ↓</button
          ><button
            class="primary"
            disabled={busy || !items.some((i) => i.state === "selected")}
            onclick={optimize}
            >{busy ? "Uploading…" : "Optimize pending"} ↗</button
          >
        </div>
      </div>
      {#if !items.length}<div class="empty">
          <span aria-hidden="true">▧</span>
          <h3>A fresh canvas.</h3>
          <p>Your images and their lighter alternatives will appear here.</p>
        </div>{/if}
      {#each items as item (item.key)}
        <article class="image-card">
          <div class="image-heading">
            <div class="file-mark" aria-hidden="true">
              {#if item.image?.outputs.length}<img
                  class="thumbnail"
                  src={[...item.image.outputs].sort(
                    (a, b) => a.bytes - b.bytes,
                  )[0].url}
                  alt=""
                  loading="lazy"
                />{:else}▧{/if}
            </div>
            <div class="file-title">
              <h3>{item.name}</h3>
              <p>
                {bytes(item.size)}{#if item.image}
                  · {item.image.original.width} × {item.image.original
                    .height}{/if}
              </p>
            </div>
            <span
              class:complete={item.state === "completed"}
              class="status"
              role="status"
              >{item.state === "completed"
                ? "✓ Completed"
                : item.state === "uploading"
                  ? `Uploading ${item.progress}%`
                  : item.state}{#if item.state === "processing"}
                · {item.generated || 0}/{item.planned || 0} outputs{/if}</span
            >
          </div>
          {#if item.error}<p class="error">{item.error}</p>{/if}
          {#if item.state === "uploading"}<progress
              value={item.progress}
              max="100"
              aria-label={`Upload progress for ${item.name}`}
            ></progress>{/if}
          {#if item.state === "upload-failed" && item.file}<button
              onclick={() => upload(item)}>Retry same upload</button
            >{/if}
          {#if item.image && item.state === "completed"}{@const image =
              item.image}
            <div class="outputs">
              {#each [image.original.format, "webp", "avif"] as format}{@const output =
                  primary(image, format)}
                <div class="output">
                  <span class="format"
                    >{format === "jpeg" ? "JPG" : format.toUpperCase()}</span
                  >
                  <h3>{output ? bytes(output.bytes) : "Unavailable"}</h3>
                  {#if output}<p>{difference(output)}</p>
                    <small>{output.width} × {output.height}</small>
                    <div class="output-actions">
                      <button
                        onclick={() =>
                          download(output.url, filename(item, output))}
                        >Download ↓</button
                      ><button
                        class="quiet"
                        onclick={() => {
                          previewError = false;
                          preview = {
                            original: image.original_url,
                            output: output.url,
                            label: format.toUpperCase(),
                          };
                        }}>Preview</button
                      >
                    </div>{:else}<p>
                      This profile did not produce a full-size {format.toUpperCase()}.
                    </p>{/if}
                </div>{/each}
            </div>
            {#if image.outputs.some((o) => o.width !== image.original.width || o.height !== image.original.height)}<details
              >
                <summary>More sizes · responsive variants</summary
                >{#each image.outputs.filter((o) => o.width !== image.original.width || o.height !== image.original.height) as output}<div
                    class="variant"
                  >
                    <span
                      >{output.width} × {output.height} · {output.format.toUpperCase()}
                      · {bytes(output.bytes)} · {difference(output)}</span
                    ><button
                      onclick={() =>
                        download(output.url, filename(item, output))}
                      >Download ↓</button
                    >
                  </div>{/each}
              </details>{/if}
            <div class="card-footer">
              <div>
                <button
                  class="quiet"
                  onclick={() => download(image.original_url, item.name)}
                  >Untouched original ↓</button
                ><button class="quiet" onclick={() => archive([item])}
                  >All generated outputs ZIP ↓</button
                >
              </div>
              <button class="danger quiet" onclick={() => remove(item)}
                >Delete from server</button
              >
            </div>
          {:else if item.id && item.state !== "deleted"}<button
              class="danger quiet"
              onclick={() => remove(item)}>Delete from server</button
            >{/if}
        </article>{/each}
      {#if items.some((i) => i.state === "completed")}<p class="fine-print">
          ZIP archives contain generated outputs from completed images only,
          including responsive sizes. Untouched originals are separate. Up to
          160 files / 512 MiB per ZIP.
        </p>{/if}
    </section>
  {/if}
  <aside class="privacy">
    <span aria-hidden="true">⌁</span>
    <p>
      <strong>Your instance. Your images.</strong> Files are uploaded to this ByteDown
      server for processing and remain stored until you delete them. Clearing the
      view does not delete files.
    </p>
  </aside>
</main>
<footer>
  <a class="brand small" href="/"
    ><img src="/logo.svg" alt="" width="26" height="26" />ByteDown.</a
  ><span>Smaller files. More possibilities.</span><span
    >Built for the web, thoughtfully.</span
  >
</footer>
{#if preview}<dialog
    class="modal"
    aria-label="Visual image preview"
    use:openDialog
    onclose={closePreview}
    oncancel={closePreview}
  >
    <div class="section-heading">
      <h2>A closer look</h2>
      <button id="close-preview" onclick={closePreview}>Close ×</button>
    </div>
    <p>Visual preview at matching scale; not an image-quality score.</p>
    <div class="comparison">
      <figure>
        <div class="checker">
          <img src={preview.original} alt="Untouched original" />
        </div>
        <figcaption>Untouched original</figcaption>
      </figure>
      <figure>
        <div class="checker">
          <img
            src={preview.output}
            alt={`${preview.label} generated output`}
            onerror={() => (previewError = true)}
          />
        </div>
        <figcaption>
          {previewError
            ? "Preview unavailable in this browser; the download is still available."
            : preview.label}
        </figcaption>
      </figure>
    </div>
  </dialog>{/if}

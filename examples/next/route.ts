import "server-only";
import { auth } from "@/auth"; // Adapt to your application's existing session/auth module.

export const runtime = "nodejs";

export async function POST(request: Request) {
  const session = await auth();
  if (!session?.user) return new Response("Unauthorized", { status: 401 });
  const origin = request.headers.get("origin");
  if (!origin || origin !== new URL(request.url).origin) {
    return new Response("Forbidden", { status: 403 });
  }
  const key = process.env.BYTEDOWN_API_KEY;
  const base = process.env.BYTEDOWN_URL;
  if (!key || !base) return new Response("Not configured", { status: 503 });
  // Enforce request/body limits at the reverse proxy before this handler buffers a form.
  const form = await request.formData();
  const file = form.get("file");
  if (!(file instanceof File) || file.size > 20_971_520) {
    return new Response("Invalid file or over 20 MiB", { status: 400 });
  }
  const outgoing = new FormData();
  outgoing.set("file", file);
  outgoing.set("profile", "web");
  const uploaded = await fetch(new URL("/v1/images", base), {
    method: "POST",
    headers: { Authorization: ["Bearer", key].join(" ") },
    body: outgoing,
    redirect: "error",
    cache: "no-store",
    signal: AbortSignal.timeout(30_000),
  });
  if (!uploaded.ok) return new Response("Upload rejected", { status: 502 });
  const { image_id, status_url } = await uploaded.json();
  const jobURL = new URL(status_url, base);
  if (jobURL.origin !== new URL(base).origin) return new Response("Invalid status URL", { status: 502 });
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    const response = await fetch(jobURL, {
      headers: { Authorization: ["Bearer", key].join(" ") },
      redirect: "error",
      cache: "no-store",
      signal: AbortSignal.timeout(Math.max(1, Math.min(10_000, deadline - Date.now()))),
    });
    if (!response.ok) return new Response("Job lookup failed", { status: 502 });
    const job = await response.json();
    if (job.status === "failed") return new Response("Processing failed", { status: 502 });
    if (job.status === "succeeded") {
      const metadata = await fetch(new URL(`/v1/images/${encodeURIComponent(image_id)}`, base), {
        headers: { Authorization: ["Bearer", key].join(" ") },
        redirect: "error",
        cache: "no-store",
        signal: AbortSignal.timeout(10_000),
      });
      if (!metadata.ok) return new Response("Manifest unavailable", { status: 502 });
      // Persist image_id and an owner relationship in your application before returning.
      return Response.json(await metadata.json());
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  return new Response("Processing continues; retry from a server-side job", { status: 504 });
}

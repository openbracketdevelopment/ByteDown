type Variant = { format: string; width: number; height: number; url: string };
type Manifest = { outputs: Variant[] };

// Use only for intentionally public variants, or URLs rewritten to your authenticated media proxy.
export function OptimizedPicture({
  manifest, mediaOrigin, alt, sizes = "(max-width: 768px) 100vw, 768px",
}: { manifest: Manifest; mediaOrigin: string; alt: string; sizes?: string }) {
  const ordered = [...manifest.outputs].sort((a, b) => a.width - b.width);
  const fallbacks = ordered.filter((v) => ["jpeg", "jpg", "png"].includes(v.format));
  const fallback = fallbacks.at(-1);
  if (!fallback) throw new Error("Profile must include an optimized JPEG/PNG fallback");
  const url = (v: Variant) => new URL(v.url, mediaOrigin).toString();
  const srcSet = (variants: Variant[]) =>
    [...new Map(variants.map((v) => [v.width, v])).values()]
      .map((v) => `${url(v)} ${v.width}w`).join(", ");
  return (
    <picture>
      {["avif", "webp"].map((format) => {
        const variants = ordered.filter((v) => v.format === format);
        return variants.length ? (
          <source key={format} type={`image/${format}`} srcSet={srcSet(variants)} sizes={sizes} />
        ) : null;
      })}
      <img src={url(fallback)} srcSet={srcSet(fallbacks.filter((v) => v.format === fallback.format))}
        sizes={sizes} width={fallback.width} height={fallback.height} alt={alt}
        loading="lazy" decoding="async" style={{ maxWidth: "100%", height: "auto" }} />
    </picture>
  );
}

export type Output = {
  id: string;
  format: string;
  width: number;
  height: number;
  bytes: number;
  url: string;
  size_difference_percent: number;
  includes_resizing: boolean;
};
export type ImageResult = {
  id: string;
  job_id: string;
  status: string;
  original: { format: string; width: number; height: number; bytes: number };
  original_url: string;
  outputs: Output[];
};
export const bytes = (n: number) =>
  !Number.isFinite(n)
    ? "Unavailable"
    : n < 1024
      ? `${n} B`
      : `${(n / (n < 1048576 ? 1024 : 1048576)).toFixed(1)} ${n < 1048576 ? "KiB" : "MiB"}`;
export function difference(o: Output) {
  const p = o.size_difference_percent;
  return `${Math.abs(p).toFixed(1)}% ${p < 0 ? "larger" : "smaller"}${o.includes_resizing ? " · includes resizing" : ""}`;
}
export function primary(image: ImageResult, format: string) {
  return image.outputs.find(
    (o) =>
      o.format === format &&
      o.width === image.original.width &&
      o.height === image.original.height,
  );
}
export function invalid(file: Pick<File, "name" | "size">, limit: number) {
  if (!/\.(jpe?g|png)$/i.test(file.name)) return "Choose a JPG or PNG image.";
  if (!file.size) return "This file is empty.";
  if (file.size > limit) return `Exceeds the ${bytes(limit)} upload limit.`;
  return "";
}

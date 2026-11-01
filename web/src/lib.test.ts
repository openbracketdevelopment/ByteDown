import { describe, it, expect } from "vitest";
import {
  invalid,
  bytes,
  difference,
  primary,
  type Output,
  type ImageResult,
} from "./lib";
describe("batch validation", () => {
  it("accepts JPEG and PNG while reporting invalid files individually", () => {
    expect(
      [
        { name: "one.jpg", size: 10 },
        { name: "two.PNG", size: 11 },
        { name: "bad.gif", size: 12 },
      ].map((f) => invalid(f, 20)),
    ).toEqual(["", "", "Choose a JPG or PNG image."]);
  });
  it("rejects empty and oversized inputs", () => {
    expect(invalid({ name: "a.jpg", size: 0 }, 20)).toContain("empty");
    expect(invalid({ name: "a.png", size: 21 }, 20)).toContain("limit");
  });
});
describe("honest output presentation", () => {
  const o: Output = {
    id: "1",
    format: "webp",
    width: 100,
    height: 100,
    bytes: 200,
    url: "/media/1",
    size_difference_percent: -8,
    includes_resizing: false,
  };
  it("shows size increases", () => expect(difference(o)).toBe("8.0% larger"));
  it("labels resizing", () =>
    expect(difference({ ...o, includes_resizing: true })).toContain(
      "includes resizing",
    ));
  it("does not substitute resized or original files for absent full-size outputs", () => {
    const image = {
      original: { width: 200, height: 200 },
      outputs: [o],
    } as ImageResult;
    expect(primary(image, "webp")).toBeUndefined();
    expect(
      primary(
        { ...image, original: { ...image.original, width: 100, height: 100 } },
        "webp",
      ),
    ).toBe(o);
  });
  it("uses consistent binary units", () => {
    expect(bytes(1024)).toBe("1.0 KiB");
    expect(bytes(0)).toBe("0 B");
    expect(bytes(NaN)).toBe("Unavailable");
  });
});

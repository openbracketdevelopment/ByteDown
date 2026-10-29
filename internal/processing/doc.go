// Package processing validates and transforms still JPEG and PNG images.
//
// Input dimensions describe the EXIF-oriented image, not the encoded raster.
// Input EncodedWidth/EncodedHeight retain the original unrotated raster size;
// Width/Height describe its oriented display size.
// Both encoded and oriented dimensions are checked before pixel allocation.
// Untagged RGB/gray is treated as sRGB. Embedded RGB, gray and CMYK ICC profiles
// are converted to sRGB; untagged CMYK, incompatible/broken profiles and
// non-sRGB PNG gamma/chromaticity without an ICC profile are rejected rather
// than guessed. Outputs contain sRGB pixels without EXIF or other metadata.
//
// Every job includes a re-encoded, full-size copy in the input format. Profile
// IncludeSource controls whether the input width is also included among the
// WebP/AVIF variants (the application's default profile enables it).
// SizeDifferencePercent is 100*(input bytes-output bytes)/input bytes:
// positive means savings and negative means growth. IncludesResizing compares
// each output's dimensions against the oriented input dimensions.
//
// Errors are diagnostic, not safe public API messages. The caller must enforce
// wall-clock deadlines by terminating its isolated processing child process.
// Validate, Process and Probe lazily initialize libvips and belong exclusively
// in those children. Formats, Widths and Planned do not initialize libvips and
// are safe in the API/worker parent. No explicit Init or Shutdown is required.
// Each child's native concurrency defaults to one; VIPS_CONCURRENCY may specify
// an integer from one through sixteen. Invalid settings fail child startup.
//
// JPEG quality is configurable; its entropy effort is fixed to optimized
// Huffman coding, since govips exposes no numeric JPEG effort. PNG always uses
// lossless compression level nine without palette quantization. WebP and AVIF
// have independently configurable quality and numeric effort controls.
package processing

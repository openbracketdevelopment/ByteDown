// Package processing validates and transforms still JPEG and PNG images.
//
// Input dimensions describe the EXIF-oriented image, not the encoded raster.
// Both encoded and oriented dimensions are checked before pixel allocation.
// Untagged RGB/gray is treated as sRGB. Embedded RGB, gray and CMYK ICC profiles
// are converted to sRGB; untagged CMYK, incompatible/broken profiles and
// non-sRGB PNG gamma/chromaticity without an ICC profile are rejected rather
// than guessed. Outputs contain sRGB pixels without EXIF or other metadata.
//
// Every job includes a re-encoded, full-size copy in the input format. Profile
// IncludeSource controls whether the input width is also included among the
// WebP/AVIF variants (the application's default profile enables it).
// SizeDifferencePercent is 100*(output bytes-input bytes)/input bytes:
// negative means savings and positive means growth. IncludesResizing compares
// each output's dimensions against the oriented input dimensions.
//
// Errors are diagnostic, not safe public API messages. The caller must enforce
// wall-clock deadlines by terminating its isolated processing child process.
package processing

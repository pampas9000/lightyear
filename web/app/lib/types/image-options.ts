/* Generated from docs/contracts/schemas/image-options.schema.json by: task gen:types. Do not edit. */

export type Chroma = "420" | "444";
export type OutputFormat = "avif" | "jxl" | "webp" | "jpeg" | "png" | "heic";

export interface ImageOptions {
  /**
   * 8 or 10, AVIF and HEIC only; absent means follow the source.
   */
  bit_depth?: number | null;
  /**
   * AVIF, HEIC and JPEG only; absent means chosen automatically.
   */
  chroma?: Chroma | null;
  /**
   * What happens to the source's color profile.
   */
  color?: "keep" | "srgb";
  dynamic_range?: "keep" | "sdr";
  /**
   * 1 (fastest) – 10 (smallest). Not available for JPEG. Unset uses each
   * encoder's own default.
   */
  effort?: number | null;
  format: OutputFormat;
  /**
   * Not available for JPEG.
   */
  lossless?: boolean;
  /**
   * EXIF and XMP handling; the ICC profile is governed by `color`.
   */
  metadata?: "keep" | "strip";
  /**
   * 1–100. Not used with `lossless` or for PNG.
   */
  quality?: number | null;
  resize?: Resize | null;
}
export interface Resize {
  /**
   * Allow upscaling when the source is smaller than the box.
   */
  enlarge?: boolean;
  /**
   * How the image is fitted into `width` × `height`.
   */
  fit?: "inside" | "cover" | "fill";
  height?: number | null;
  width?: number | null;
}

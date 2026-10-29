// Thin wrapper over libavif so that Rust never depends on libavif's struct
// layouts: the compiler resolves them here against the installed header.

#include <avif/avif.h>
#include <stdio.h>
#include <string.h>

typedef struct {
    const uint8_t *pixels;
    uint32_t width;
    uint32_t height;
    uint32_t row_bytes;
    uint32_t channels;    // 3 (RGB) or 4 (RGBA, straight alpha)
    uint32_t pixel_depth; // 8 or 16 bits per sample in `pixels`
    uint32_t depth;       // 8, 10 or 12 bits in the AV1 stream
    int yuv444;
    int lossless;
    int quality; // 0–100, ignored when lossless
    int speed;   // 0–10
    int threads;
    const uint8_t *icc;
    size_t icc_len;
    uint8_t *exif; // its orientation is reset to 1 in place
    size_t exif_len;
    const uint8_t *xmp;
    size_t xmp_len;
} ly_avif_input;

static int fail(char *err, size_t err_len, const char *what, avifResult result, const avifEncoder *encoder) {
    snprintf(err, err_len, "%s: %s%s%s", what, avifResultToString(result),
             encoder && encoder->diag.error[0] ? ": " : "", encoder ? encoder->diag.error : "");
    return -1;
}

int ly_avif_encode(const ly_avif_input *in, uint8_t **out, size_t *out_len, char *err, size_t err_len) {
    avifPixelFormat format = in->lossless || in->yuv444 ? AVIF_PIXEL_FORMAT_YUV444 : AVIF_PIXEL_FORMAT_YUV420;
    avifImage *image = avifImageCreate(in->width, in->height, in->depth, format);
    avifEncoder *encoder = NULL;
    avifRWData output = AVIF_DATA_EMPTY;
    avifResult result;
    int status = -1;
    if (!image) {
        snprintf(err, err_len, "avifImageCreate failed");
        return -1;
    }

    image->yuvRange = AVIF_RANGE_FULL;
    if (in->lossless) {
        // Lossless needs RGB coded as-is: identity matrix, 4:4:4, quality 100.
        image->matrixCoefficients = AVIF_MATRIX_COEFFICIENTS_IDENTITY;
    } else {
        // avifImageCreate leaves the matrix unspecified, which decoders may
        // read differently; BT.601 is what avifenc writes.
        image->matrixCoefficients = AVIF_MATRIX_COEFFICIENTS_BT601;
    }
    if (in->icc_len) {
        if ((result = avifImageSetProfileICC(image, in->icc, in->icc_len)) != AVIF_RESULT_OK) {
            fail(err, err_len, "ICC profile", result, NULL);
            goto done;
        }
    } else {
        // Untagged pixels are sRGB, as avifenc assumes for untagged input.
        image->colorPrimaries = AVIF_COLOR_PRIMARIES_BT709;
        image->transferCharacteristics = AVIF_TRANSFER_CHARACTERISTICS_SRGB;
    }
    if (in->exif_len) {
        // The pixels are already upright; stop viewers from rotating again.
        size_t offset;
        if (avifGetExifOrientationOffset(in->exif, in->exif_len, &offset) == AVIF_RESULT_OK && offset < in->exif_len) {
            in->exif[offset] = 1;
        }
        if ((result = avifImageSetMetadataExif(image, in->exif, in->exif_len)) != AVIF_RESULT_OK) {
            fail(err, err_len, "Exif", result, NULL);
            goto done;
        }
    }
    if (in->xmp_len && (result = avifImageSetMetadataXMP(image, in->xmp, in->xmp_len)) != AVIF_RESULT_OK) {
        fail(err, err_len, "XMP", result, NULL);
        goto done;
    }

    avifRGBImage rgb;
    avifRGBImageSetDefaults(&rgb, image);
    rgb.format = in->channels == 4 ? AVIF_RGB_FORMAT_RGBA : AVIF_RGB_FORMAT_RGB;
    rgb.depth = in->pixel_depth;
    rgb.pixels = (uint8_t *)in->pixels;
    rgb.rowBytes = in->row_bytes;
    if ((result = avifImageRGBToYUV(image, &rgb)) != AVIF_RESULT_OK) {
        fail(err, err_len, "RGB to YUV", result, NULL);
        goto done;
    }

    if (!(encoder = avifEncoderCreate())) {
        snprintf(err, err_len, "avifEncoderCreate failed");
        goto done;
    }
    encoder->codecChoice = AVIF_CODEC_CHOICE_AOM;
    encoder->maxThreads = in->threads;
    encoder->speed = in->speed;
    // qualityAlpha keeps libavif's default: the color quality, as in avifenc.
    encoder->quality = in->lossless ? AVIF_QUALITY_LOSSLESS : in->quality;
    if (in->lossless) {
        encoder->qualityAlpha = AVIF_QUALITY_LOSSLESS;
    }
    if ((result = avifEncoderWrite(encoder, image, &output)) != AVIF_RESULT_OK) {
        fail(err, err_len, "encode", result, encoder);
        goto done;
    }
    *out = output.data;
    *out_len = output.size;
    output.data = NULL;
    status = 0;

done:
    avifRWDataFree(&output);
    if (encoder) {
        avifEncoderDestroy(encoder);
    }
    avifImageDestroy(image);
    return status;
}

void ly_avif_free(uint8_t *data) {
    avifRWData d = {data, 0};
    avifRWDataFree(&d);
}

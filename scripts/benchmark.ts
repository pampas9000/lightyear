import { spawnSync } from "child_process";
import { existsSync, readdirSync, readFileSync, statSync } from "fs";
import { join } from "path";

/**
 * Fixture inventory and header-level fidelity checks.
 *
 * Everything reported here is measured from the fixture bytes. There are no
 * unconditional "PASS" lines: a check either observes a real value or reports
 * SKIP with the reason, and any FAIL (or unreadable fixture, or failing Rust
 * suite) makes the process exit non-zero.
 */

const fixturesRoot = join(import.meta.dir, "fixtures");
const extDir = join(fixturesRoot, "external");
const hdDir = join(fixturesRoot, "hd_benchmark");
const synthDir = join(fixturesRoot, "quality");

if (!existsSync(extDir)) {
    console.error("External fixtures directory not found. Please run fixture download/generation first.");
    process.exit(1);
}

const failures: string[] = [];
const skips: string[] = [];
let checks = 0;

type Probe = {
    format: string;
    width?: number;
    height?: number;
    bitDepth?: number;
    hasIcc?: boolean;
    exifOrientation?: number;
    cicp?: string;
};

// ---------------------------------------------------------------------------
// Header probes (pure byte parsing, no image dependencies)
// ---------------------------------------------------------------------------

function probePng(data: Buffer): Probe {
    const probe: Probe = { format: "png" };
    let offset = 8;
    while (offset + 8 <= data.length) {
        const length = data.readUInt32BE(offset);
        const type = data.toString("latin1", offset + 4, offset + 8);
        const body = offset + 8;
        if (body + length > data.length) break;

        if (type === "IHDR") {
            probe.width = data.readUInt32BE(body);
            probe.height = data.readUInt32BE(body + 4);
            probe.bitDepth = data[body + 8];
        } else if (type === "iCCP") {
            probe.hasIcc = true;
        } else if (type === "eXIf") {
            const orientation = tiffOrientation(data.subarray(body, body + length));
            if (orientation !== undefined) probe.exifOrientation = orientation;
        } else if (type === "IEND") {
            break;
        }
        offset = body + length + 4;
    }
    return probe;
}

function probeJpeg(data: Buffer): Probe {
    const probe: Probe = { format: "jpeg" };
    let offset = 2;
    while (offset + 4 <= data.length) {
        if (data[offset] !== 0xff) break;
        const marker = data[offset + 1];
        if (marker === 0xda || marker === 0xd9) break;
        const length = data.readUInt16BE(offset + 2);
        if (length < 2 || offset + 2 + length > data.length) break;
        const payload = data.subarray(offset + 4, offset + 2 + length);

        // SOF0..SOF15 (excluding DHT/DAC/JPG markers) carry the frame size.
        if (marker >= 0xc0 && marker <= 0xcf && marker !== 0xc4 && marker !== 0xc8 && marker !== 0xcc) {
            probe.bitDepth = payload[0];
            probe.height = payload.readUInt16BE(1);
            probe.width = payload.readUInt16BE(3);
        } else if (marker === 0xe2 && payload.subarray(0, 12).toString("latin1") === "ICC_PROFILE\u0000") {
            probe.hasIcc = true;
        } else if (marker === 0xe1 && payload.subarray(0, 6).toString("latin1") === "Exif\u0000\u0000") {
            const orientation = tiffOrientation(payload.subarray(6));
            if (orientation !== undefined) probe.exifOrientation = orientation;
        }
        offset += 2 + length;
    }
    return probe;
}

function probeWebp(data: Buffer): Probe {
    const probe: Probe = { format: "webp" };
    let offset = 12;
    while (offset + 8 <= data.length) {
        const fourcc = data.toString("latin1", offset, offset + 4);
        const length = data.readUInt32LE(offset + 4);
        const body = offset + 8;
        if (body + length > data.length) break;

        if (fourcc === "VP8X") {
            probe.width = 1 + (data[body + 4] | (data[body + 5] << 8) | (data[body + 6] << 16));
            probe.height = 1 + (data[body + 7] | (data[body + 8] << 8) | (data[body + 9] << 16));
        } else if (fourcc === "VP8L") {
            const bits = data.readUInt32LE(body + 1);
            probe.width = 1 + (bits & 0x3fff);
            probe.height = 1 + ((bits >> 14) & 0x3fff);
        } else if (fourcc === "VP8 ") {
            probe.width = data.readUInt16LE(body + 6) & 0x3fff;
            probe.height = data.readUInt16LE(body + 8) & 0x3fff;
        } else if (fourcc === "ICCP") {
            probe.hasIcc = true;
        } else if (fourcc === "EXIF") {
            const raw = data.subarray(body, body + length);
            const exif = raw.subarray(0, 6).toString("latin1") === "Exif\u0000\u0000" ? raw.subarray(6) : raw;
            const orientation = tiffOrientation(exif);
            if (orientation !== undefined) probe.exifOrientation = orientation;
        }
        offset = body + length + (length & 1);
    }
    return probe;
}

function probeIsobmff(data: Buffer): Probe {
    const probe: Probe = { format: data.toString("latin1", 8, 12) };
    const boxes = walkBoxes(data, [
        ["meta", "ispe"],
        ["meta", "pixi"],
        ["meta", "colr"],
    ]);
    const ispe = boxes.get("ispe");
    if (ispe) {
        probe.width = ispe.readUInt32BE(4);
        probe.height = ispe.readUInt32BE(8);
    }
    const pixi = boxes.get("pixi");
    if (pixi) probe.bitDepth = pixi[5];

    const colr = boxes.get("colr");
    if (colr && colr.toString("latin1", 0, 4) === "nclx") {
        const primaries = colr.readUInt16BE(4);
        const transfer = colr.readUInt16BE(6);
        const matrix = colr.readUInt16BE(8);
        probe.cicp = `${primaries}/${transfer}/${matrix}`;
    }
    return probe;
}

/**
 * Walks ISOBMFF boxes descending through `meta` (a full box) and the
 * `iprp`/`ipco` containers, collecting the first payload of each wanted type.
 */
function walkBoxes(data: Buffer, wanted: Array<[string, string]>): Map<string, Buffer> {
    const found = new Map<string, Buffer>();
    // `wanted` is [parent, child] pairs; index by the child type being searched.
    const targets = new Map(wanted.map(([parent, child]) => [child, parent]));

    const descend = (buf: Buffer, parent: string, depth: number) => {
        if (depth > 4) return;
        let offset = 0;
        while (offset + 8 <= buf.length) {
            const size = buf.readUInt32BE(offset);
            const type = buf.toString("latin1", offset + 4, offset + 8);
            if (size < 8 || offset + size > buf.length) return;
            const payload = buf.subarray(offset + 8, offset + size);

            if (targets.get(type) === parent && !found.has(type)) {
                found.set(type, payload);
            }
            if (type === "meta" && payload.length > 4) {
                descend(payload.subarray(4), "meta", depth + 1);
            } else if (type === "iprp" || type === "ipco") {
                descend(payload, "meta", depth + 1);
            }
            offset += size;
        }
    };

    descend(data, "", 0);
    return found;
}

/** Reads the orientation tag (0x0112) out of a TIFF/EXIF header. */
function tiffOrientation(tiff: Buffer): number | undefined {
    if (tiff.length < 8) return undefined;
    const little = tiff.toString("latin1", 0, 2) === "II";
    const u16 = (at: number) => (little ? tiff.readUInt16LE(at) : tiff.readUInt16BE(at));
    const u32 = (at: number) => (little ? tiff.readUInt32LE(at) : tiff.readUInt32BE(at));

    if (u16(2) !== 42) return undefined;
    const ifd = u32(4);
    const entries = u16(ifd);
    for (let i = 0; i < entries; i++) {
        const entry = ifd + 2 + i * 12;
        if (entry + 12 > tiff.length) return undefined;
        if (u16(entry) === 0x0112) return u16(entry + 8);
    }
    return undefined;
}

/** JPEG XL: only the container/codestream signature is recognised here. */
function probeJxl(): Probe {
    return { format: "jxl" };
}

function probeFile(path: string): Probe | undefined {
    const data = readFileSync(path);
    if (data.length < 12) return undefined;
    if (data.subarray(0, 8).toString("latin1") === "\x89PNG\r\n\x1a\n") return probePng(data);
    if (data[0] === 0xff && data[1] === 0xd8) return probeJpeg(data);
    if (data.toString("latin1", 0, 4) === "RIFF" && data.toString("latin1", 8, 12) === "WEBP") return probeWebp(data);
    if (data.toString("latin1", 4, 8) === "ftyp" || data.toString("latin1", 4, 8) === "meta") return probeIsobmff(data);
    if (data[0] === 0xff && data[1] === 0x0a) return probeJxl();
    if (data.length >= 12 && data.subarray(0, 12).toString("latin1") === "\x00\x00\x00\x0cJXL \r\n\x87\n") {
        return probeJxl();
    }
    return undefined;
}

// ---------------------------------------------------------------------------
// Checks
// ---------------------------------------------------------------------------

function check(label: string, ok: boolean, detail: string) {
    checks++;
    if (ok) {
        console.log(`  -> [PASS] ${label}: ${detail}`);
    } else {
        console.log(`  -> [FAIL] ${label}: ${detail}`);
        failures.push(`${label}: ${detail}`);
    }
}

function skip(label: string, reason: string) {
    console.log(`  -> [SKIP] ${label}: ${reason}`);
    skips.push(`${label}: ${reason}`);
}

const listFiles = (dir: string): string[] =>
    existsSync(dir) ? readdirSync(dir).filter((f) => !f.startsWith(".")) : [];

const allEntries: Array<[string, string]> = [
    ...listFiles(extDir).map((f) => [extDir, f] as [string, string]),
    ...listFiles(hdDir).map((f) => [hdDir, f] as [string, string]),
    ...listFiles(synthDir).map((f) => [synthDir, f] as [string, string]),
];

console.log("=================================================================");
console.log("   Lightyear Fixture Header Audit & Fidelity Test");
console.log("=================================================================\n");

console.log(`[1/3] Inspecting ${allEntries.length} fixture headers\n`);
const probes = new Map<string, Probe>();
for (const [dir, file] of allEntries) {
    const path = join(dir, file);
    const sizeKb = (statSync(path).size / 1024).toFixed(1);
    const probe = probeFile(path);
    if (!probe) {
        check(file, false, `header could not be parsed [${sizeKb} KB]`);
        continue;
    }
    probes.set(file, probe);

    // Record genuine coverage gaps instead of inventing a pass.
    if (probe.format === "jxl") {
        skip(file, "JXL image header introspection is not implemented; covered by cargo test");
    } else if (probe.width === undefined || probe.height === undefined) {
        skip(file, "dimensions could not be read from the header");
    } else if (probe.format === "heic" && probe.bitDepth === undefined) {
        skip(file, "HEIC bit depth requires parsing av1C; covered by cargo test");
    }

    const dims = probe.width && probe.height ? `${probe.width}x${probe.height}` : "?x?";
    const tags = [
        probe.bitDepth ? `${probe.bitDepth}-bit` : undefined,
        probe.hasIcc ? "ICC" : undefined,
        probe.exifOrientation ? `orient=${probe.exifOrientation}` : undefined,
        probe.cicp ? `CICP ${probe.cicp}` : undefined,
    ].filter(Boolean);
    console.log(
        `  * ${file.padEnd(36)} ${probe.format.padEnd(6)} ${dims.padStart(11)} ${sizeKb.padStart(9)} KB  ${tags.join(" ")}`,
    );
}

console.log("\n[2/3] Measured fidelity assertions\n");

const png16 = probes.get("aom_weld_16bit.png");
if (png16) {
    check(
        "16-bit source retains precision",
        (png16.bitDepth ?? 0) > 8,
        `aom_weld_16bit.png is ${png16.bitDepth}-bit, expected > 8`,
    );
} else {
    check("16-bit source retains precision", false, "aom_weld_16bit.png is missing from the corpus");
}

const p3Logo = probes.get("webkit_logo_p3.png");
if (p3Logo) {
    check(
        "wide-gamut source carries an ICC profile",
        p3Logo.hasIcc === true,
        `webkit_logo_p3.png hasIcc=${p3Logo.hasIcc}`,
    );
} else {
    check("wide-gamut source carries an ICC profile", false, "webkit_logo_p3.png is missing from the corpus");
}

const rec2020 = probes.get("aom_seine_hdr_rec2020.avif");
if (rec2020) {
    check(
        "HDR AVIF is 10-bit and tagged with CICP",
        rec2020.bitDepth === 10 || rec2020.bitDepth === 12,
        `aom_seine_hdr_rec2020.avif is ${rec2020.bitDepth}-bit, CICP=${rec2020.cicp ?? "none"}`,
    );
    check(
        "HDR AVIF advertises BT.2020 primaries",
        rec2020.cicp?.startsWith("9/") === true,
        `expected CICP primaries 9 (BT.2020), got ${rec2020.cicp ?? "none"}`,
    );
} else {
    check("HDR AVIF headers", false, "aom_seine_hdr_rec2020.avif is missing from the corpus");
}

const exifOriented = allEntries
    .map(([, file]) => [file, probes.get(file)] as const)
    .filter(([, probe]) => probe?.exifOrientation !== undefined);
if (exifOriented.length > 0) {
    console.log(
        `  -> [INFO] ${exifOriented.length} fixture(s) carry an EXIF orientation tag: ` +
            exifOriented.map(([file, probe]) => `${file}=${probe?.exifOrientation}`).join(", "),
    );
} else {
    skip("EXIF orientation fixture coverage", "no fixture in the corpus carries an orientation tag");
}

console.log("\n[3/3] Running the engine fidelity suite (cargo test --workspace)\n");
const cargo = spawnSync("cargo", ["test", "--workspace", "--quiet"], {
    cwd: join(import.meta.dir, ".."),
    encoding: "utf-8",
});
if (cargo.error) {
    check("engine fidelity suite", false, `could not run cargo: ${cargo.error.message}`);
} else if (cargo.status !== 0) {
    const tail = (cargo.stdout ?? "").split("\n").slice(-25).join("\n");
    check("engine fidelity suite", false, `cargo test exited ${cargo.status}\n${tail}`);
} else {
    check("engine fidelity suite", true, "cargo test --workspace passed");
}

console.log("\n=================================================================");
console.log(`  Checks: ${checks}  Failures: ${failures.length}  Skips: ${skips.length}`);
console.log("=================================================================");

if (skips.length > 0) {
    console.log("\nSkipped (not a pass — coverage gap):");
    for (const entry of skips) console.log(`  * ${entry}`);
}

if (failures.length > 0) {
    console.log("\nFailures:");
    for (const entry of failures) console.log(`  * ${entry}`);
    process.exit(1);
}

"""Compare the server's AVIF paths with avifenc on image-qa's images.

Three paths, all speed 6 and metadata stripped:
  libavif  lightyear as it runs: libvips decodes and prepares pixels, libavif encodes
  libvips  lightyear with WORKER_AVIF_ENCODER=libvips: heifsave, effort 4 (aom speed 6)
  avifenc  `avifenc -s 6`, taken from image-qa's database where its settings match

Two parts:
  quality  encode at 9 Q values, score with image-qa's own decode + SSIMULACRA2 +
           Butteraugli, and compare with avifenc at the same Q
  decode   at Q80, check whether lightyear's libavif output is byte-identical to
           avifenc reading the PNG/JPEG itself with tiling off

Run with image-qa's Python environment, which provides `iqa.tools.metrics`:
  task bench:avif IMAGE_QA=~/path/to/image-qa
Results go to target/avif-encoders/ (results.json and the encoded files).
"""

import json
import math
import os
import sqlite3
import subprocess
import sys
import tempfile
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

IQA = Path(os.environ["IMAGE_QA"]).expanduser()
sys.path.insert(0, str(IQA / "backend/src"))
from iqa.tools import metrics  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
PROCESS = ROOT / "target/release/examples/process"
OUT = ROOT / "target/avif-encoders"
IMAGES = IQA / "data/images"
DB = IQA / "data/iqa.sqlite"

# The 12 images of docs/engineering-notes/encoder-comparison.md.
NAMES = ["p_night", "p_clic1", "i26-latte-art", "commons-nikon-d90", "dsc01027", "p_map",
         "p_shot2", "i26w-terminal-nvim-edit", "j_ph1", "j_ph5", "j_ani1", "j_ani2"]
QS = [95, 92, 90, 87, 84, 80, 75, 70, 62]
# image-qa's avifenc results do not match these two, so avifenc is run here:
# it encoded the 16-bit image at 12 bits (lightyear allows 8 or 10), and the
# transparent one with lossless alpha (libavif's default follows the color quality).
RERUN_AVIFENC = {"dsc01027": ["-d", "10"], "commons-nikon-d90": []}


def source(name):
    # JPEG sources are read as JPEG, as image-qa's avifenc runs did.
    return IMAGES / name / ("original.jpg" if name.startswith("j_") else "ref.png")


def lightyear(src, out, encoder, chroma, q):
    opts = {"format": "avif", "quality": q, "chroma": chroma, "metadata": "strip"}
    if encoder == "libvips":
        opts["effort"] = 4  # heifsave effort 3 = aom speed 6
    subprocess.run([str(PROCESS), str(src), json.dumps(opts), str(out)], check=True,
                   capture_output=True, env={**os.environ, "WORKER_AVIF_ENCODER": encoder})


def avifenc(src, out, chroma, q, extra=()):
    subprocess.run(["avifenc", "-s", "6", "-y", chroma, "-q", str(q), *extra, str(src), str(out)],
                   check=True, capture_output=True)


def encode_and_score(name, encoder, chroma, q):
    out = OUT / name / f"{encoder}_{chroma}" / f"q{q}.avif"
    out.parent.mkdir(parents=True, exist_ok=True)
    if encoder == "avifenc":
        avifenc(source(name), out, chroma, q, RERUN_AVIFENC[name])
    else:
        lightyear(source(name), out, encoder, chroma, q)
    s, b = metrics(IMAGES / name / "ref.png", out)
    return {"image": name, "encoder": encoder, "chroma": chroma, "q": q,
            "bytes": out.stat().st_size, "ssimulacra2": s, "butteraugli": b}


def chromas(name):
    # The 4:2:0 JPEGs are also tested at 4:2:0, where avifenc reads their YCbCr directly.
    return ["444", "420"] if name in ("j_ph1", "j_ph5") else ["444"]


def quality(workers):
    jobs = [(n, e, c, q) for n in NAMES for c in chromas(n) for e in ("libavif", "libvips") for q in QS]
    jobs += [(n, "avifenc", "444", q) for n in RERUN_AVIFENC for q in QS]
    with ThreadPoolExecutor(workers) as pool:
        rows = list(pool.map(lambda j: encode_and_score(*j), jobs))
    db = sqlite3.connect(DB)
    for name in NAMES:
        if name in RERUN_AVIFENC:
            continue
        for chroma in chromas(name):
            for q, size, s, b in db.execute(
                    "select param, bytes, ssimulacra2, butteraugli from encodes "
                    "where image_id=? and preset_id=? and status='done'", (name, f"avif{chroma}_s6")):
                rows.append({"image": name, "encoder": "avifenc", "chroma": chroma, "q": q,
                             "bytes": size, "ssimulacra2": s, "butteraugli": b})
    return rows


def compare(rows):
    """At the same Q: geometric mean of size ÷ avifenc, mean SSIMU2 and Butteraugli difference.
    Same-Q pairs are used rather than interpolation at equal quality: with 9 points the
    Butteraugli (max-norm) curve is too noisy to interpolate."""
    def find(name, encoder, chroma):
        return {r["q"]: r for r in rows if (r["image"], r["encoder"], r["chroma"]) == (name, encoder, chroma)}
    print(f"\n{'image':26}{'chroma':8}{'encoder':9}{'size ÷ avifenc':>15}{'ΔSSIMU2':>9}{'ΔBA':>8}")
    for name in NAMES:
        for chroma in chromas(name):
            base = find(name, "avifenc", chroma)
            for encoder in ("libavif", "libvips"):
                pairs = [(r, base[q]) for q, r in find(name, encoder, chroma).items() if q in base]
                n = len(pairs)
                size = math.exp(sum(math.log(r["bytes"] / a["bytes"]) for r, a in pairs) / n)
                ds = sum(r["ssimulacra2"] - a["ssimulacra2"] for r, a in pairs) / n
                db = sum(r["butteraugli"] - a["butteraugli"] for r, a in pairs) / n
                print(f"{name:26}{chroma:8}{encoder:9}{size:15.3f}{ds:+9.2f}{db:+8.3f}")


def decode():
    """lightyear (libvips decode → libavif) against avifenc decoding the file itself,
    tiling off so that only the decoder differs."""
    print(f"\n{'image':26}{'chroma':8}{'lightyear':>10}{'avifenc':>10}  identical")
    with tempfile.TemporaryDirectory() as tmp:
        ly, ae = Path(tmp) / "ly.avif", Path(tmp) / "ae.avif"
        for name in NAMES:
            for chroma in (["444", "420"] if name.startswith("j_") else ["444"]):
                lightyear(source(name), ly, "libavif", chroma, 80)
                extra = ["--tilerowslog2", "0", "--tilecolslog2", "0", "--ignore-exif", "--ignore-xmp"]
                avifenc(source(name), ae, chroma, 80, extra + RERUN_AVIFENC.get(name, []))
                same = ly.read_bytes() == ae.read_bytes()
                print(f"{name:26}{chroma:8}{ly.stat().st_size:10}{ae.stat().st_size:10}  {'yes' if same else 'no'}")


if __name__ == "__main__":
    OUT.mkdir(parents=True, exist_ok=True)
    rows = quality(int(os.environ.get("JOBS", "4")))
    (OUT / "results.json").write_text(json.dumps(rows, indent=1))
    compare(rows)
    decode()

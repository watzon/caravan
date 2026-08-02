#!/usr/bin/env python3
"""An independent yEnc implementation, used to keep the Go corpus honest.

The generated fixtures in this directory are produced by Go's own encoder
(see ../corpus_test.go), so on their own they would only prove that Caravan's
encoder and decoder agree with each other. This script closes that circle two
ways, and neither of them runs during `go test` — it is a checked-in tool, run
by hand when the corpus changes:

    python3 reference.py emit     # writes reference-single.yenc / .bin
    python3 reference.py verify   # decodes every fixture and checks it

`emit` writes an article encoded by this file, not by Go: a different line
length (96), a narrower escaping policy (only the four characters the format
requires, plus a leading '.'), and no trailing-whitespace escaping. Go's
decoder reads it in TestDecodeCorpus, which is what proves the decoder handles
other people's articles.

`verify` decodes every generated .yenc fixture with this implementation and
compares the result against the committed .bin payloads, which is what proves
Go's *encoder* emits articles a foreign decoder accepts. Run it after
regenerating the corpus:

    go test ./internal/usenet/yenc -run Corpus -update
    python3 internal/usenet/yenc/testdata/reference.py verify
"""

import binascii
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

CRITICAL = {0x00, 0x0A, 0x0D, 0x3D}


def encode(name, data, line_length=96):
    """Encode a single-part yEnc article body."""
    out = [b"=ybegin line=%d size=%d name=%s\r\n" % (line_length, len(data), name.encode())]
    line = bytearray()
    for b in data:
        c = (b + 42) % 256
        if c in CRITICAL or (len(line) == 0 and c == 0x2E):
            line += bytes([0x3D, (c + 64) % 256])
        else:
            line.append(c)
        if len(line) >= line_length:
            out.append(bytes(line) + b"\r\n")
            line = bytearray()
    if line:
        out.append(bytes(line) + b"\r\n")
    crc = binascii.crc32(data) & 0xFFFFFFFF
    out.append(b"=yend size=%d crc32=%08x\r\n" % (len(data), crc))
    return b"".join(out)


def encode_multipart(name, data, part_size, line_length=96):
    """Encode a multipart yEnc posting, returning the article bodies.

    The offset convention is written out from the yEnc 1.3 draft rather than
    copied from Go: `=ypart begin=`/`end=` are 1-based and inclusive on both
    ends, so part n covering data[o:o+k] declares begin=o+1 end=o+k. The last
    part carries the whole-file crc32 in addition to its own pcrc32.
    """
    total = (len(data) + part_size - 1) // part_size
    file_crc = binascii.crc32(data) & 0xFFFFFFFF
    out = []
    for n in range(total):
        offset = n * part_size
        chunk = data[offset:offset + part_size]
        lines = [
            b"=ybegin part=%d total=%d line=%d size=%d name=%s\r\n"
            % (n + 1, total, line_length, len(data), name.encode()),
            b"=ypart begin=%d end=%d\r\n" % (offset + 1, offset + len(chunk)),
        ]
        line = bytearray()
        for b in chunk:
            c = (b + 42) % 256
            if c in CRITICAL or (len(line) == 0 and c == 0x2E):
                line += bytes([0x3D, (c + 64) % 256])
            else:
                line.append(c)
            if len(line) >= line_length:
                lines.append(bytes(line) + b"\r\n")
                line = bytearray()
        if line:
            lines.append(bytes(line) + b"\r\n")
        trailer = b"=yend size=%d part=%d pcrc32=%08x" % (
            len(chunk), n + 1, binascii.crc32(chunk) & 0xFFFFFFFF)
        if n == total - 1:
            trailer += b" crc32=%08x" % file_crc
        lines.append(trailer + b"\r\n")
        out.append(b"".join(lines))
    return out


def decode(article):
    """Decode one yEnc article body, returning (headers, payload, trailer)."""
    lines = article.split(b"\n")
    header = None
    trailer = None
    payload = bytearray()
    escaped = False
    for raw in lines:
        line = raw[:-1] if raw.endswith(b"\r") else raw
        if header is None:
            if line.startswith(b"=ybegin"):
                header = keywords(line[len(b"=ybegin"):])
            continue
        if line.startswith(b"=ypart"):
            header.update(keywords(line[len(b"=ypart"):]))
            continue
        if line.startswith(b"=yend"):
            trailer = keywords(line[len(b"=yend"):])
            break
        for b in line:
            if escaped:
                payload.append((b - 64 - 42) % 256)
                escaped = False
            elif b == 0x3D:
                escaped = True
            else:
                payload.append((b - 42) % 256)
    return header, bytes(payload), trailer


def keywords(rest):
    text = rest.decode("latin-1").strip()
    out = {}
    while text:
        text = text.lstrip()
        if text.lower().startswith("name="):
            out["name"] = text[5:].strip()
            break
        field, _, text = text.partition(" ")
        key, eq, value = field.partition("=")
        if eq:
            out[key.lower()] = value
    return out


def reference_payload():
    """A payload with every byte value and a run of the awkward ones."""
    data = bytearray()
    for _ in range(3):
        data += bytes(range(256))
    # Bytes whose encoded value is a critical character or a leading dot.
    data += bytes([0x04, 0xD6, 0xE0, 0xE3, 0x13, 0xDF, 0xF6]) * 8
    data += b"caravan yenc reference article\r\n"
    return bytes(data)


def reference_multi_payload():
    """A payload that splits into three parts, the last one short."""
    data = bytearray()
    for _ in range(2):
        data += bytes(range(256))
    data += b"caravan yenc multipart reference\r\n"
    return bytes(data)


REFERENCE_MULTI_PART = 200


def emit():
    payload = reference_payload()
    write("reference-single.bin", payload)
    write("reference-single.yenc", encode("reference.bin", payload))
    print("wrote reference-single.yenc (%d bytes) and reference-single.bin (%d bytes)"
          % (len(read("reference-single.yenc")), len(payload)))

    multi = reference_multi_payload()
    write("reference-multi.bin", multi)
    articles = encode_multipart("reference-multi.bin", multi, REFERENCE_MULTI_PART)
    if len(articles) != 3:
        raise SystemExit("expected 3 reference parts, got %d" % len(articles))
    for i, article in enumerate(articles):
        write("reference-multi.part%d.yenc" % (i + 1), article)
    print("wrote reference-multi.part1..3.yenc and reference-multi.bin (%d bytes)" % len(multi))


VALID = {
    "single.yenc": "caravan.single.bin",
    "multi.part1.yenc": None,
    "multi.part2.yenc": None,
    "multi.part3.yenc": None,
    "escaped.yenc": "escaped.bin",
    "reference-single.yenc": "reference-single.bin",
    "reference-multi.part1.yenc": None,
    "reference-multi.part2.yenc": None,
    "reference-multi.part3.yenc": None,
}


def verify():
    failures = 0
    for fixture, expected in VALID.items():
        header, payload, trailer = decode(read(fixture))
        if header is None or trailer is None:
            print("FAIL %s: no =ybegin/=yend" % fixture)
            failures += 1
            continue

        crc = binascii.crc32(payload) & 0xFFFFFFFF
        declared = trailer.get("pcrc32") or trailer.get("crc32")
        if declared is not None and int(declared, 16) != crc:
            print("FAIL %s: crc %08x, article says %s" % (fixture, crc, declared))
            failures += 1
        if int(trailer.get("size", len(payload))) != len(payload):
            print("FAIL %s: size %d, article says %s" % (fixture, len(payload), trailer["size"]))
            failures += 1

        if expected is not None:
            want = read(expected)
            if payload != want:
                print("FAIL %s: payload differs from %s" % (fixture, expected))
                failures += 1
        print("ok   %s (%d bytes, crc %08x)" % (fixture, len(payload), crc))

    # The multipart fixtures have to reassemble into the whole file.
    for parts, source in (
        (("multi.part1.yenc", "multi.part2.yenc", "multi.part3.yenc"), "caravan.multi.bin"),
        (("reference-multi.part1.yenc", "reference-multi.part2.yenc",
          "reference-multi.part3.yenc"), "reference-multi.bin"),
    ):
        whole = read(source)
        out = bytearray(len(whole))
        for fixture in parts:
            header, payload, _ = decode(read(fixture))
            begin = int(header["begin"]) - 1
            out[begin:begin + len(payload)] = payload
        if bytes(out) != whole:
            print("FAIL multipart reassembly differs from %s" % source)
            failures += 1
        else:
            print("ok   multipart reassembly of %s (%d bytes)" % (source, len(whole)))

    if failures:
        print("%d failure(s)" % failures)
        return 1
    print("corpus verified by the reference implementation")
    return 0


def read(name):
    with open(os.path.join(HERE, name), "rb") as fh:
        return fh.read()


def write(name, data):
    with open(os.path.join(HERE, name), "wb") as fh:
        fh.write(data)


if __name__ == "__main__":
    command = sys.argv[1] if len(sys.argv) > 1 else "verify"
    if command == "emit":
        emit()
    elif command == "verify":
        sys.exit(verify())
    else:
        print(__doc__)
        sys.exit(2)

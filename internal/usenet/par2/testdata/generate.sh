#!/usr/bin/env bash
#
# Rebuild the par2 fixture corpus from scratch using the reference
# implementation, par2cmdline.
#
#     PAR2_BIN=/opt/homebrew/bin/par2 ./generate.sh
#
# Everything this writes is committed, and `go test ./internal/usenet/par2`
# reads only the committed output — the tests never shell out to par2. That
# split is the point: the corpus is the reference implementation's opinion,
# frozen, and Caravan's job is to agree with it. Repair math that is subtly
# wrong corrupts media silently (PLAN phase 7 risks), so "our decoder and our
# encoder agree with each other" is worth nothing here; only agreement with a
# foreign implementation is evidence.
#
# What lands in this directory:
#
#   sets/<set>/original/      pristine source files, byte-for-byte
#   sets/<set>/par2/          the par2 set created over them
#   sets/<set>/cases/<case>/  only the files a case damages
#   reference/<set>/<case>.*  par2cmdline's verbatim verify/repair output
#   MANIFEST.json             the machine-readable verdicts the Go tests assert
#
# A case is rebuilt at test time by copying original/ and par2/ into a temp
# directory, overwriting with cases/<case>/, and deleting the case's
# removed_files. Damaged bytes are committed rather than recomputed so that a
# drifting damage recipe can never quietly change what the tests exercise.
#
# The script also repairs each repairable case with par2cmdline and checks the
# result against the originals. If the reference cannot reproduce its own
# inputs, the corpus is broken and nothing downstream is worth running.
#
# Re-running this reproduces sets/ and MANIFEST.json byte for byte. The logs
# under reference/ can differ between runs because par2cmdline is threaded and
# interleaves its per-file lines; they are provenance for the numbers in the
# manifest, and nothing asserts against them.

set -euo pipefail

PAR2_BIN=${PAR2_BIN:-par2}
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

command -v "$PAR2_BIN" >/dev/null || {
	echo "generate.sh: $PAR2_BIN not found; set PAR2_BIN" >&2
	exit 1
}
command -v python3 >/dev/null || {
	echo "generate.sh: python3 is required" >&2
	exit 1
}

par2_version=$("$PAR2_BIN" -V 2>&1 | head -1)
echo "generate.sh: using $par2_version"

rm -rf "$here/sets" "$here/reference"
mkdir -p "$here/sets" "$here/reference"
: >"$work/records.jsonl"

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

# gen_file PATH SEED SIZE — deterministic pseudo-random bytes. Python's
# Mersenne Twister is stable across versions and platforms, so re-running this
# script anywhere reproduces the same corpus.
gen_file() {
	python3 - "$1" "$2" "$3" <<-'PY'
		import random, sys
		path, seed, size = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
		random.seed(seed)
		with open(path, "wb") as f:
		    f.write(bytes(random.getrandbits(8) for _ in range(size)))
	PY
}

# zero_range PATH OFFSET LENGTH — the "a chunk of the file came back as zeros"
# damage, which is what a yEnc segment that never arrived looks like once the
# assembler has written the file sparse.
zero_range() {
	python3 - "$1" "$2" "$3" <<-'PY'
		import sys
		path, off, n = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
		with open(path, "r+b") as f:
		    f.seek(off)
		    f.write(b"\0" * n)
	PY
}

# flip_range PATH OFFSET LENGTH — same length, different bytes. Distinct from
# zeroing because a zeroed slice is a plausible "all zeros" payload while a
# flipped one is unambiguous corruption.
flip_range() {
	python3 - "$1" "$2" "$3" <<-'PY'
		import sys
		path, off, n = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
		with open(path, "r+b") as f:
		    f.seek(off)
		    chunk = f.read(n)
		    f.seek(off)
		    f.write(bytes(b ^ 0xFF for b in chunk))
	PY
}

truncate_to() {
	python3 -c 'import os,sys; os.truncate(sys.argv[1], int(sys.argv[2]))' "$1" "$2"
}

# strip_progress SRC DST — par2cmdline redraws a percentage counter with CR,
# which turns into unreadable noise once redirected to a file. Drop those
# fragments so the committed reference logs are the verdict and nothing else.
strip_progress() {
	python3 - "$1" "$2" <<-'PY'
		import re, sys
		src, dst = sys.argv[1], sys.argv[2]
		raw = open(src, encoding="utf-8", errors="replace").read()
		progress = re.compile(r"[A-Za-z][A-Za-z ]*: [\d.]+%")
		out = []
		for line in raw.split("\n"):
		    parts = [p for p in line.split("\r") if not progress.fullmatch(p)]
		    kept = "".join(parts)
		    if not kept and line:
		        continue  # the whole line was a redrawn progress counter
		    out.append(kept)
		open(dst, "w", encoding="utf-8").write("\n".join(out))
	PY
}

# ---------------------------------------------------------------------------
# make_set NAME BLOCKSIZE RECOVERYBLOCKS EXTRA_CREATE_ARGS -- FILE:SEED:SIZE...
# ---------------------------------------------------------------------------
make_set() {
	local name=$1 blocksize=$2 recovery=$3 extra=$4
	shift 4

	local src="$work/$name/src"
	mkdir -p "$src"
	local spec file seed size
	for spec in "$@"; do
		IFS=: read -r file seed size <<<"$spec"
		gen_file "$src/$file" "$seed" "$size"
	done

	# Create in a directory holding only the sources so the par2 set records
	# bare filenames and nothing else gets swept in.
	(
		cd "$src"
		# shellcheck disable=SC2086
		"$PAR2_BIN" create -q -q -s"$blocksize" -c"$recovery" $extra "$name.par2" ./*
	)

	mkdir -p "$here/sets/$name/original" "$here/sets/$name/par2"
	for spec in "$@"; do
		IFS=: read -r file _ _ <<<"$spec"
		cp "$src/$file" "$here/sets/$name/original/$file"
	done
	mv "$src"/*.par2 "$here/sets/$name/par2/"

	CURRENT_SET=$name
	CURRENT_BLOCKSIZE=$blocksize
	CURRENT_RECOVERY=$recovery
}

# ---------------------------------------------------------------------------
# make_case CASE DESCRIPTION -- OP...
#
# OP is one of
#   zero:FILE:OFF:LEN   zero LEN bytes at OFF
#   flip:FILE:OFF:LEN   invert LEN bytes at OFF
#   trunc:FILE:SIZE     truncate to SIZE
#   grow:FILE:LEN       append LEN bytes of 0xAA
#   rm:FILE             delete the file entirely
# ---------------------------------------------------------------------------
make_case() {
	local set_name=$CURRENT_SET
	local case_name=$1 description=$2
	shift 2

	local dir="$work/$set_name/cases/$case_name"
	rm -rf "$dir"
	mkdir -p "$dir"
	cp "$here/sets/$set_name/original"/* "$dir/"
	cp "$here/sets/$set_name/par2"/* "$dir/"

	local removed=()
	local touched=()
	local op kind file a b
	for op in "$@"; do
		IFS=: read -r kind file a b <<<"$op"
		case "$kind" in
		zero) zero_range "$dir/$file" "$a" "$b" ;;
		flip) flip_range "$dir/$file" "$a" "$b" ;;
		trunc) truncate_to "$dir/$file" "$a" ;;
		grow) python3 -c 'import sys; open(sys.argv[1],"ab").write(b"\xaa"*int(sys.argv[2]))' "$dir/$file" "$a" ;;
		rm)
			rm -f "$dir/$file"
			removed+=("$file")
			;;
		*)
			echo "make_case: unknown op $kind" >&2
			exit 1
			;;
		esac
		if [ "$kind" != rm ]; then
			touched+=("$file")
		fi
	done

	mkdir -p "$here/reference/$set_name"
	local verify_log="$here/reference/$set_name/$case_name.verify.txt"
	local repair_log="$here/reference/$set_name/$case_name.repair.txt"
	rm -f "$repair_log"

	local verify_status=0
	(cd "$dir" && "$PAR2_BIN" verify -- "$set_name.par2") >"$verify_log.raw" 2>&1 || verify_status=$?
	strip_progress "$verify_log.raw" "$verify_log"
	rm -f "$verify_log.raw"

	# Stash the damaged files before repair rewrites them in place.
	mkdir -p "$here/sets/$set_name/cases/$case_name"
	rm -f "$here/sets/$set_name/cases/$case_name"/*
	local f
	for f in "${touched[@]+"${touched[@]}"}"; do
		cp "$dir/$f" "$here/sets/$set_name/cases/$case_name/$f"
	done

	local repair_status=""
	local repair_ok=""
	if [ "$verify_status" -eq 1 ]; then
		repair_status=0
		(cd "$dir" && "$PAR2_BIN" repair -- "$set_name.par2") >"$repair_log.raw" 2>&1 || repair_status=$?
		strip_progress "$repair_log.raw" "$repair_log"
		rm -f "$repair_log.raw"
		repair_ok=true
		for f in "$here/sets/$set_name/original"/*; do
			if ! cmp -s "$f" "$dir/$(basename "$f")"; then
				repair_ok=false
			fi
		done
		if [ "$repair_ok" != true ] || [ "$repair_status" -ne 0 ]; then
			echo "generate.sh: reference repair FAILED for $set_name/$case_name" >&2
			exit 1
		fi
	fi

	# Record the verdict. The parsing lives in python so the numbers in
	# MANIFEST.json are lifted straight out of par2cmdline's own words, which
	# are committed next to them in reference/.
	python3 - \
		"$work/records.jsonl" "$set_name" "$case_name" "$description" \
		"$verify_log" "$verify_status" "${repair_status:-}" \
		"$CURRENT_BLOCKSIZE" "$CURRENT_RECOVERY" \
		"$(printf '%s\n' "${removed[@]+"${removed[@]}"}" | paste -sd, -)" \
		"$(printf '%s\n' "${touched[@]+"${touched[@]}"}" | paste -sd, -)" \
		<<-'PY'
			import json, re, sys

			(out, set_name, case_name, description, verify_log, verify_status,
			 repair_status, blocksize, recovery, removed, touched) = sys.argv[1:12]

			text = open(verify_log, encoding="utf-8", errors="replace").read()

			def find(pattern, *groups):
			    m = re.search(pattern, text)
			    if not m:
			        return None if len(groups) == 1 else None
			    return tuple(int(m.group(g)) for g in groups)

			total = find(r"There are a total of (\d+) data blocks", 1)
			total_blocks = total[0] if total else None

			data = find(r"You have (\d+) out of (\d+) data blocks available", 1, 2)
			if data:
			    good_blocks, total_from_summary = data
			    if total_blocks is not None and total_from_summary != total_blocks:
			        raise SystemExit(f"generate.sh: block totals disagree for {set_name}/{case_name}")
			else:
			    # par2cmdline only prints the availability line when something is
			    # wrong; an undamaged set has every block.
			    good_blocks = total_blocks

			rec = find(r"You have (\d+) recovery blocks available", 1)
			recovery_available = rec[0] if rec else int(recovery)
			deficit = find(r"You need (\d+) more recovery blocks", 1)
			deficit = deficit[0] if deficit else 0
			excess = find(r"You have an excess of (\d+) recovery blocks", 1)
			excess = excess[0] if excess else None

			complete = "All files are correct, repair is not required." in text
			possible = "Repair is possible." in text
			impossible = "Repair is not possible." in text

			if complete:
			    verdict = "complete"
			elif possible:
			    verdict = "repairable"
			elif impossible:
			    verdict = "unrepairable"
			else:
			    raise SystemExit(f"generate.sh: cannot classify verify output for {set_name}/{case_name}:\n{text}")

			# The number of slices that have to be reconstructed is the shortfall
			# in data blocks, which is exactly what our repair solves for.
			slices_needed = total_blocks - good_blocks

			# Which files the reference considers usable as-is. par2cmdline has
			# several phrasings for "not usable" ("damaged", "missing", "no data
			# found"), all of which are the same thing to a repairer, so the
			# manifest records the positive set and the test derives the rest.
			files_ok = sorted(set(re.findall(r'Target: "([^"]+)" - found\.', text)))
			# Where par2cmdline printed a per-file block count, keep it: the
			# corpus damages data in place, so its scan and a positional scan
			# have to agree block for block.
			blocks_found = {
			    m.group(1): [int(m.group(2)), int(m.group(3))]
			    for m in re.finditer(r'Target: "([^"]+)" - damaged\. Found (\d+) of (\d+) data blocks\.', text)
			}

			record = {
			    "set": set_name,
			    "case": case_name,
			    "description": description,
			    "block_size": int(blocksize),
			    "recovery_blocks_created": int(recovery),
			    "damaged_files": [f for f in touched.split(",") if f],
			    "removed_files": [f for f in removed.split(",") if f],
			    "reference": {
			        "verdict": verdict,
			        "verify_exit_code": int(verify_status),
			        "repair_exit_code": int(repair_status) if repair_status else None,
			        "data_blocks_total": total_blocks,
			        "data_blocks_good": good_blocks,
			        "slices_needed": slices_needed,
			        "recovery_blocks_available": recovery_available,
			        "block_deficit": deficit,
			        "excess_recovery_blocks": excess,
			        "files_ok": files_ok,
			        "file_blocks_found": blocks_found,
			    },
			}
			with open(out, "a", encoding="utf-8") as f:
			    f.write(json.dumps(record, sort_keys=True) + "\n")
		PY

	echo "  case $set_name/$case_name"
}

# ---------------------------------------------------------------------------
# The corpus
# ---------------------------------------------------------------------------

# "basic": three files at a 1024-byte block size, one of them deliberately not
# a whole number of blocks (20000 = 19 blocks + 544 bytes) and one smaller
# than a single block. 25 data blocks, 10 recovery blocks, spread over the
# default variable-sized recovery volumes so the parser has to stitch a set
# together out of several files.
make_set basic 1024 10 "" \
	alpha.bin:11:20000 \
	beta.bin:22:4096 \
	gamma.bin:33:1000

make_case pristine "undamaged; verification alone must report nothing to do"

make_case zeroed-range "300 zeroed bytes straddling two slices of alpha.bin" \
	zero:alpha.bin:5000:300

make_case flipped-byte "a single inverted byte inside one slice of alpha.bin" \
	flip:alpha.bin:9000:1

make_case truncated "beta.bin cut short mid-slice" \
	trunc:beta.bin:2500

make_case missing-file "gamma.bin absent entirely" \
	rm:gamma.bin

make_case sub-block-file "gamma.bin damaged; its only slice is shorter than the block size" \
	flip:gamma.bin:100:50

make_case tail-slice "the short final slice of alpha.bin is destroyed" \
	zero:alpha.bin:19500:500

make_case multi-damage "all three files broken at once, still inside capacity" \
	zero:alpha.bin:5000:300 \
	trunc:beta.bin:2500 \
	rm:gamma.bin

make_case at-capacity "exactly 10 slices destroyed against exactly 10 recovery blocks" \
	zero:alpha.bin:0:10240

make_case unrepairable "13 slices destroyed against 10 recovery blocks" \
	zero:alpha.bin:0:13312

make_case all-missing "every source file gone; far beyond capacity" \
	rm:alpha.bin rm:beta.bin rm:gamma.bin

# "small": a single file at a 512-byte block size in one recovery volume. The
# different block size is what keeps the implementation from accidentally
# hard-coding 1024 anywhere, and the single-volume layout exercises the
# non-variable recovery file path.
make_set small 512 4 "-n1" \
	delta.bin:44:3000

make_case pristine "undamaged single-file set"

make_case zeroed-range "two slices of delta.bin zeroed" \
	zero:delta.bin:600:900

make_case at-capacity "four slices destroyed against four recovery blocks" \
	zero:delta.bin:0:2048

make_case unrepairable "five slices destroyed against four recovery blocks" \
	zero:delta.bin:0:2560

# ---------------------------------------------------------------------------
# MANIFEST.json
# ---------------------------------------------------------------------------

python3 - "$here" "$work/records.jsonl" "$par2_version" <<-'PY'
	import hashlib, json, os, sys

	here, records_path, par2_version = sys.argv[1], sys.argv[2], sys.argv[3]

	records = [json.loads(line) for line in open(records_path, encoding="utf-8")]

	sets = {}
	order = []
	for rec in records:
	    name = rec["set"]
	    if name not in sets:
	        order.append(name)
	        original = os.path.join(here, "sets", name, "original")
	        par2dir = os.path.join(here, "sets", name, "par2")
	        files = []
	        for fn in sorted(os.listdir(original)):
	            blob = open(os.path.join(original, fn), "rb").read()
	            files.append({
	                "name": fn,
	                "size": len(blob),
	                "sha256": hashlib.sha256(blob).hexdigest(),
	                "md5": hashlib.md5(blob).hexdigest(),
	            })
	        sets[name] = {
	            "name": name,
	            "block_size": rec["block_size"],
	            "recovery_blocks_created": rec["recovery_blocks_created"],
	            "index_file": name + ".par2",
	            "par2_files": sorted(os.listdir(par2dir)),
	            "files": files,
	            "cases": [],
	        }
	    case = {k: v for k, v in rec.items() if k not in ("set", "block_size", "recovery_blocks_created")}
	    sets[name]["cases"].append(case)

	manifest = {
	    "generated_by": par2_version,
	    "note": (
	        "Reference verdicts recorded by par2cmdline. Tests assert agreement "
	        "with these numbers and never invoke the par2 binary. Regenerate "
	        "with ./generate.sh."
	    ),
	    "sets": [sets[n] for n in order],
	}
	with open(os.path.join(here, "MANIFEST.json"), "w", encoding="utf-8") as f:
	    json.dump(manifest, f, indent=2, sort_keys=True)
	    f.write("\n")
	print("generate.sh: wrote MANIFEST.json")
PY

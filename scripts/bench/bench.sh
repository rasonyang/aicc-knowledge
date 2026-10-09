#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Benchmark TEI embedding and reranking on this host with synthetic text only.
# Starts one TEI container at a time, measures it with kb-bench, stops it.
# Works on Linux and macOS with Docker; the only other thing it needs is the
# kb-bench binary (BENCH_BIN, bin/kb-bench, or built here when Go is present).
#
# Usage: scripts/bench/bench.sh [options]
#   --embed-url URL     measure an already running embedding TEI instead of starting one
#   --rerank-url URL    measure an already running reranker TEI (needs --rerank-model)
#   --rerank-model ID   model id recorded for --rerank-url
#   --no-embed          skip the embedding phase
#   --no-rerank         skip the reranker phases
#   --quick             small sample sizes (smoke test, not for numbers)
#   --out DIR           result directory (default bench-results/<host>-<timestamp>)
#   -h, --help
#
# Environment:
#   BENCH_BIN            path of the kb-bench binary
#   BENCH_CACHE          model cache directory, mounted at /data (default ~/.cache/kb-bench)
#   BENCH_IMAGE          TEI image (default cpu-1.9.4 on x86_64, cpu-arm64-1.9.4 on arm64)
#   BENCH_EMBED_MODEL    default BAAI/bge-m3
#   BENCH_RERANKERS      space-separated reranker models (default BAAI/bge-reranker-v2-m3;
#                        add BAAI/bge-reranker-base for the small one)
#   BENCH_PORT_EMBED     host port of the embed container (default 18188)
#   BENCH_PORT_RERANK    host port of the reranker container (default 18189)
#   BENCH_N_EMBED / BENCH_N_RERANK   sample sizes (default 200 / 100)
#   BENCH_CONCURRENCY    comma-separated levels, empty for none (default 2,4)
#   BENCH_WAIT           seconds to wait for a model to load (default 1800)
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
if [ -d "$here/../../cmd/kb-bench" ]; then cd "$here/../.."; else cd "$here"; fi # repo checkout, or a copied script

embed_url="" rerank_url="" rerank_model="" do_embed=1 do_rerank=1 quick=0 out=""
while [ $# -gt 0 ]; do
	case "$1" in
	--embed-url) embed_url=$2; shift 2 ;;
	--rerank-url) rerank_url=$2; shift 2 ;;
	--rerank-model) rerank_model=$2; shift 2 ;;
	--no-embed) do_embed=0; shift ;;
	--no-rerank) do_rerank=0; shift ;;
	--quick) quick=1; shift ;;
	--out) out=$2; shift 2 ;;
	-h | --help) sed -n '3,32p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "unknown option: $1" >&2; exit 2 ;;
	esac
done

bin=${BENCH_BIN:-}
if [ -z "$bin" ]; then
	if [ -x bin/kb-bench ]; then bin=bin/kb-bench
	elif [ -x ./kb-bench ]; then bin=./kb-bench
	elif command -v go >/dev/null 2>&1; then
		mkdir -p bin && CGO_ENABLED=0 go build -trimpath -o bin/kb-bench ./cmd/kb-bench && bin=bin/kb-bench
	else
		echo "kb-bench not found and Go is missing. Build it elsewhere:" >&2
		echo "  make bench-dist   # then copy bin/kb-bench-linux-amd64 here and set BENCH_BIN" >&2
		exit 1
	fi
fi

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) default_image=ghcr.io/huggingface/text-embeddings-inference:cpu-1.9.4 ;;
arm64 | aarch64) default_image=ghcr.io/huggingface/text-embeddings-inference:cpu-arm64-1.9.4 ;;
*) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
image=${BENCH_IMAGE:-$default_image}
embed_model=${BENCH_EMBED_MODEL:-BAAI/bge-m3}
rerankers=${BENCH_RERANKERS:-BAAI/bge-reranker-v2-m3}
port_embed=${BENCH_PORT_EMBED:-18188}
port_rerank=${BENCH_PORT_RERANK:-18189}
cache=${BENCH_CACHE:-$HOME/.cache/kb-bench}
wait_sec=${BENCH_WAIT:-1800}
conc=${BENCH_CONCURRENCY-2,4}
n_embed=${BENCH_N_EMBED:-200}
n_rerank=${BENCH_N_RERANK:-100}
conc_n_embed=0 conc_n_rerank=0
if [ "$quick" = 1 ]; then n_embed=20 n_rerank=5 conc_n_embed=8 conc_n_rerank=4; fi

host=$(hostname -s 2>/dev/null || hostname)
[ -n "$out" ] || out=bench-results/$host-$(date +%Y%m%d-%H%M%S)
mkdir -p "$out" "$cache"

container=""
cleanup() { [ -z "$container" ] || docker rm -f "$container" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

# start_tei NAME PORT MODEL: runs a TEI container, waits on /info, prints the
# seconds it took to answer.
start_tei() {
	local name=$1 port=$2 model=$3 t0 t1
	docker rm -f "$name" >/dev/null 2>&1 || true
	container=$name
	t0=$(date +%s)
	docker run -d --name "$name" -p "127.0.0.1:$port:80" -v "$cache:/data" "$image" \
		--model-id "$model" --max-batch-tokens 4096 >/dev/null
	"$bin" wait --url "http://127.0.0.1:$port" --timeout "${wait_sec}s" >/dev/null || {
		docker logs --tail 30 "$name" >&2 || true
		return 1
	}
	t1=$(date +%s)
	echo $((t1 - t0))
}
stop_tei() { docker rm -f "$1" >/dev/null 2>&1 || true; container=""; }

if [ -n "$embed_url" ] || [ -n "$rerank_url" ]; then
	"$bin" host --out "$out"
else
	"$bin" host --out "$out" --image "$image"
fi
echo "results: $out" >&2

measure() { # KIND URL MODEL STARTUP N CONCN
	"$bin" measure --kind "$1" --url "$2" --model "$3" --startup-sec "$4" --n "$5" --conc-n "$6" \
		--concurrency "$conc" --image "${7:-}" --out "$out"
}

if [ "$do_embed" = 1 ]; then
	if [ -n "$embed_url" ]; then
		measure embed "$embed_url" "$embed_model" 0 "$n_embed" "$conc_n_embed" "(existing endpoint)"
	else
		echo "starting $embed_model ..." >&2
		s=$(start_tei kb-bench-embed "$port_embed" "$embed_model")
		measure embed "http://127.0.0.1:$port_embed" "$embed_model" "$s" "$n_embed" "$conc_n_embed" "$image"
		stop_tei kb-bench-embed
	fi
fi

if [ "$do_rerank" = 1 ]; then
	if [ -n "$rerank_url" ]; then
		[ -n "$rerank_model" ] || { echo "--rerank-url needs --rerank-model" >&2; exit 2; }
		measure rerank "$rerank_url" "$rerank_model" 0 "$n_rerank" "$conc_n_rerank" "(existing endpoint)"
	elif [ -z "$embed_url" ] || [ -n "${BENCH_FORCE_RERANK:-}" ]; then
		for m in $rerankers; do
			echo "starting $m ..." >&2
			s=$(start_tei kb-bench-rerank "$port_rerank" "$m")
			measure rerank "http://127.0.0.1:$port_rerank" "$m" "$s" "$n_rerank" "$conc_n_rerank" "$image"
			stop_tei kb-bench-rerank
		done
	else
		echo "skipping rerankers: --embed-url given without --rerank-url (set BENCH_FORCE_RERANK=1 to start them)" >&2
	fi
fi

"$bin" summary --out "$out"

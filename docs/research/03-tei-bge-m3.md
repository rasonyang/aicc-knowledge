# TEI (CPU) serving BAAI/bge-m3 - live probe (2026-10-08)

Verdict: CAVEAT (works, native arm64, ~31 ms p50 single query on Apple silicon; needs `--max-batch-tokens` lowered or the container OOMs at warm-up on a 12.5 GB Docker VM; x86 numbers still need a separate run).

## Image
- Latest release: v1.9.4 (https://github.com/huggingface/text-embeddings-inference/releases/tag/v1.9.4)
- README (https://github.com/huggingface/text-embeddings-inference#docker-images) lists: x86_64 `cpu-1.9`, aarch64 `cpu-arm64-1.9`.
- `cpu-1.9.4` (x86): manifest has amd64 only. `cpu-arm64-1.9.4` / `cpu-arm64-1.9`: manifest has linux/arm64 (plus an attestation entry).
- Used: `ghcr.io/huggingface/text-embeddings-inference:cpu-arm64-1.9.4`
  - index digest sha256:798cd7615e060a72ccbdf19aa0ada52289c6a0147490195bafaece6ce04d70f1, arm64 manifest sha256:7a1b4aa11c491f39cea38eb0a210afe68a78dd5edee11c8f44f46c3529b27cd8, image 472 MB.
- Native run, NOT emulated. A native x86 run (`cpu-1.9.4`) is still needed for production-like numbers (different ISA: AVX2/AVX512 vs NEON).

## Model support
- README supported-models table includes XLM-RoBERTa embedding models (bge-m3 is XLM-RoBERTa, 568M); /info reports model_id BAAI/bge-m3, version 1.9.4, dtype float32.
- Pooling: bge-m3 `1_Pooling/config.json` has `pooling_mode_cls_token: true`, dim 1024 (https://huggingface.co/BAAI/bge-m3/raw/main/1_Pooling/config.json). TEI reads this file automatically (README: "If pooling is not set, parsed from 1_Pooling/config.json"), so `--pooling cls` is NOT needed. The log showed it downloading `1_Pooling/config.json`.
- License: MIT (model card https://huggingface.co/BAAI/bge-m3).
- TEI loaded the ONNX weights (`onnx/model.onnx` + `model.onnx_data`, ~2.2 GB) and logged "Backend does not support a batch size > 8" (see batch note). Max input length reported 4096 with my flag (model supports 8192).

## Exact command
```
docker run -d --name tei -p 18080:80 -v $SCRATCH/tei/cache:/data \
  ghcr.io/huggingface/text-embeddings-inference:cpu-arm64-1.9.4 \
  --model-id BAAI/bge-m3 --max-batch-tokens 4096
```
- Port 18080 used because 8080 is taken by a local `aicc` process and an `rms` container (health check on 8080 would have given false positives).
- First attempt with the default max-batch-tokens (16384) was OOM-killed (exit 137, OOMKilled=true) during "Warming up model", with ~11 GB free in the Docker VM. `--max-batch-tokens 4096` fixed it. No CPU/mem limits set on the container (limit = VM: 8 CPUs, 11.65 GiB).

## Evidence
```
POST /embed {"inputs":"How do I reset my password?"}  -> n=1 dim=1024 L2=1.0000 [-0.0188, 0.0329, -0.0579]
POST /embed {"inputs":"我想查询一下我的账单"}            -> n=1 dim=1024 L2=1.0000
POST /embed {"inputs":[...2 items]}                   -> n=2 dim=1024 L2=1.0000
POST /embed {"inputs":"...","normalize":false}        -> dim=1024 L2=1.0000   (!)
```
- Vectors are L2-normalized by default (`normalize` defaults to true). Observed: `normalize:false` still returned a unit-norm vector with this ONNX backend, so do not rely on that flag to get raw vectors (observation; not further investigated).

## Latency (client-side, python urllib, time.perf_counter, localhost, native arm64 CPU, 8 CPUs)
15 mixed EN/ZH short call-center questions (5-30 tokens), cycled; 10 warm-up requests, then 200 sequential.

| Case | n | p50 | p90 | p99 | max | mean |
|---|---|---|---|---|---|---|
| single query | 200 | 30.8 ms | 38.8 ms | 45.1 ms | 54.8 ms | 31.0 ms (min 23.0) |
| batch of 8 (one request) | 20 | 163.2 ms | - | - | 176.1 ms | - |

Script: docs/research/probes/tei_bench.py.

## Resources
- Startup with cached weights: 19 s to /health OK (includes ~10 s warm-up).
- First start (download of ~2.2 GB ONNX weights): ~700 s download (network-bound) before warm-up.
- Memory: 6.19 GiB RSS-ish (docker stats) at idle after warm-up and after the benchmark, with max-batch-tokens 4096. At default 16384 it exceeded the VM and was OOM-killed. Plan >= 8 GB RAM for the container.
- Idle CPU 0.9%.

## Caveats
- Numbers are for Apple silicon native arm64; not representative of a production x86 box. Run `cpu-1.9.4` on the target x86 host with the same bench.py (change URL port).
- Batch of 8 takes ~163 ms (about 20 ms/item, no real batching gain on CPU); logged "Backend does not support a batch size > 8" for the ONNX backend.
- Host cache dir showed empty from the host side (Docker file-sharing quirk), but the container reused the weights on restart (download 0.0008 s). Cache path: <scratchpad>/tei/cache (if a later run re-downloads, the mount is not persisting).
- Only dense embeddings tested; bge-m3 sparse/ColBERT outputs are not exposed by TEI.
- Container stopped and removed.

## x86 (amd64) under emulation
Goal: confirm the amd64 image `cpu-1.9.4` runs and get an indicative latency on an Apple-silicon Mac. Emulation (Rosetta translation, QEMU TCG) does not represent real x86 server latency; treat these numbers as an upper-bound-ish indication only. Same flags as above (`--model-id BAAI/bge-m3 --max-batch-tokens 4096`), same bench, 8 CPUs / 16 GiB VM, shared model cache dir.

Image: `ghcr.io/huggingface/text-embeddings-inference:cpu-1.9.4`, digest `sha256:2538ea1c9640d3763b15af668039d24172d063b42337b0c27796fc2be180c78d` (linux/amd64).

### Commands
```bash
# Mode 1: Rosetta (Apple Virtualization.framework VM, aarch64 kernel, x86 userland translated)
colima start -p x86rosetta --arch aarch64 --vm-type vz --vz-rosetta --cpu 8 --memory 16 --mount <cache>:w
DOCKER_HOST=unix://$HOME/.colima/x86rosetta/docker.sock \
  docker run -d --name tei --platform linux/amd64 -p 18081:80 -v <cache>:/data \
  ghcr.io/huggingface/text-embeddings-inference:cpu-1.9.4 --model-id BAAI/bge-m3 --max-batch-tokens 4096
python3 docs/research/probes/tei_bench.py http://localhost:18081/embed

# Mode 2: full x86_64 VM (QEMU TCG software emulation; needs `brew install lima-additional-guestagents`)
colima start -p x86qemu --arch x86_64 --vm-type qemu --cpu 8 --memory 16 --mount <cache>:w
DOCKER_HOST=unix://$HOME/.colima/x86qemu/docker.sock \
  docker run -d --name tei -p 18082:80 -v <cache>:/data \
  ghcr.io/huggingface/text-embeddings-inference:cpu-1.9.4 --model-id BAAI/bge-m3 --max-batch-tokens 4096
python3 docs/research/probes/tei_bench.py http://localhost:18082/embed
```
Note: `colima start` switches the global docker context to the new profile; use `DOCKER_HOST` per command and restore the context afterwards (`docker context use <previous>`), and delete the profile with `colima delete -p <profile> --force`.

### Evidence
- Rosetta: `uname -m` in the amd64 container prints `x86_64`; `/proc/cpuinfo` model name `VirtualApple @ 2.50GHz`, flags include `avx avx2 fma f16c` (no avx512).
- QEMU: `uname -m` prints `x86_64`; model name `QEMU TCG CPU version 2.5+`, flags include `avx avx2 fma f16c`.
- Both: the x86 image used the ONNX backend (`Downloading onnx/model.onnx`), logged `Backend does not support a batch size > 8`, and `/embed` returned 1024-dim vectors with L2 norm 1.0 for an English and a Chinese query. The English query vector under Rosetta and QEMU had cosine 0.9999999 (identical up to float noise).
- The ONNX weights are the same ones the native arm64 run used, so no cross-architecture semantic drift is expected; a direct cosine against the arm64 container was not run.

### Latency (client-side, 10 warm-ups, 200 sequential single queries, 20x batch of 8)
| Mode | single p50 | p90 | p99 | max | batch-of-8 p50 | batch-of-8 max |
|---|---|---|---|---|---|---|
| native arm64 (reference) | 30.8 ms | 38.8 | 45.1 | 54.8 | 163 ms | - |
| amd64 under Rosetta | 58.8 ms | 83.0 | 244.1 | 311.8 | 397 ms | 410 ms |
| amd64 under QEMU TCG | 2395 ms | 3332 | 6010 | 7060 | 19961 ms | 30037 ms |

Rosetta mean 67.9 ms (min 36.0). QEMU mean 2509 ms (min 1618).

### Resources
- Memory (docker stats): 6.27 GiB (Rosetta), 6.17 GiB (QEMU), same as native.
- Startup to /health: Rosetta 713 s on first run, of which 640 s was the ONNX download and about 25 s warm-up. QEMU 1293 s with the cache already warm, almost all of it model warm-up under TCG (about 20 min); the QEMU VM itself took about 5 min to create and boot.
- QEMU TCG is unusable for any latency-sensitive purpose (seconds per query, 10 cores pegged); Rosetta is roughly 2x the native arm64 latency with a heavier tail.

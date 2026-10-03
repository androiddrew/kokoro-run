# kokoro-run

Local **US/UK English text-to-speech** in Go, using Kokoro and ONNX Runtime: a CLI
and an **OpenAI-compatible HTTP server** (`POST /v1/audio/speech`), with Docker
images for CPU and CUDA. Runs on CPU or CUDA with a neural or eSpeak pronunciation
fallback. It is a single Go binary: no Python or other interpreter is needed at run
time. Numbers, dates, clock times, money, units, URLs and markdown are read aloud
as words (see [Text preparation](#text-preparation)).

## Install

**Release binary (Linux amd64 or arm64).** Download `kokoro-run_<version>_linux_<arch>.tar.gz`
from [GitHub Releases](https://github.com/androiddrew/kokoro-run/releases) and
check it against the release's `SHA256SUMS`:

```bash
sha256sum -c --ignore-missing SHA256SUMS
tar -xzf kokoro-run_v0.1.0_linux_amd64.tar.gz   # or _linux_arm64
```

The archive holds the `kokoro-run` binary plus LICENSE, NOTICE, THIRD_PARTY.md and
`third_party/`. It needs glibc 2.34 or newer (Ubuntu 22.04, Debian 12, RHEL 9 or
later); ONNX Runtime and the Kokoro model files are installed separately (below).
On arm64, use ONNX Runtime's `linux-aarch64` package, which is CPU-only; CUDA on
arm64 (e.g. Jetson) needs an ONNX Runtime build with CUDA from another source.

**From source.** Go **1.25** or newer, CGO and a C compiler are required:

```bash
go install github.com/androiddrew/kokoro-run/cmd/kokoro-run@latest
# or, from a checkout:
go build -o bin/kokoro-run ./cmd/kokoro-run
```

The examples below use `bin/kokoro-run`, the path a checkout build produces.

## Runtime assets

Download the Kokoro model and voices (about 354 MB, checked against their SHA-256)
with `pull`, or install them by hand as [ASSETS.md](ASSETS.md) describes, then
configure:

```bash
export KOKORO_RUN_ASSETS=/absolute/path/to/runtime-assets
bin/kokoro-run pull                              # into $KOKORO_RUN_ASSETS/kokoro/
export KOKORO_RUN_ORT_LIBRARY=libonnxruntime.so  # or an explicit installed-library path
bin/kokoro-run doctor --provider cpu
bin/kokoro-run synth --text 'Hello, world!' --output hello.wav
```

`KOKORO_RUN_ASSETS` holds only the Kokoro model and voices and defaults to `assets`
relative to the current directory; `--model` and `--voices` override it. The
pronunciation dictionaries, POS model, neural fallback models and Kokoro
vocabulary are built into the binary.
Output files must be new; their parent directories must exist.

Install ONNX Runtime independently (system packages or another installation of
your choice). A bare library name uses the OS loader's configured paths; the
application does not download the runtime or change loader settings. For CUDA,
make that runtime's provider dependencies discoverable **before** launching Go.
A compatible NVIDIA driver is required. ORT 1.22.0 with CUDA 12.8/cuDNN 9.8 is
the known tested baseline, not an enforced native-version equality requirement.
`doctor --provider cuda` validates real synthesis and both Kokoro
session placements, requiring GPU Conv/MatMul and reporting CPU assignments.
Provider registration alone is insufficient. Unavailable requested CUDA fails.

## Commands

```bash
bin/kokoro-run voices
bin/kokoro-run voices --language gb --json
bin/kokoro-run phonemize --text 'Hello, world!' --json
bin/kokoro-run synth --file speech.txt --output speech.wav
bin/kokoro-run synth --file - --output stdin.wav < speech.txt
bin/kokoro-run synth --fallback espeak --language gb --voice bf_emma --provider cuda \
  --text 'Welcome to the demonstration.' --output welcome.wav
bin/kokoro-run doctor --provider cuda --log doctor-cuda.log
bin/kokoro-run bench --text 'Hello, world!' --output artifacts/local-bench
bin/kokoro-run serve --listen 127.0.0.1:8880
bin/kokoro-run pull
bin/kokoro-run config --config my.yaml
./synth-all-voices.sh
```

`synth`, `phonemize` and `bench` accept `--text`, `--file`, or stdin. The English
voice list includes `a*` US and `b*` UK voices. Defaults are `af_heart` / `bf_emma`.
A voice given without `--language` selects its own dialect; an explicit mismatch fails. `synth` writes 24 kHz mono PCM16. Inline phoneme
overrides such as `Use [Kubernetes](/kˌubəɹnˈɛtiz/) today.` and stress overrides are supported.

| Common flag | Meaning |
|---|---|
| `--ort` | User-installed ORT library path or loader name, or `KOKORO_RUN_ORT_LIBRARY` |
| `--data`, `--frontend-models` | Optional overrides for the built-in frontend data and models |
| `--model`, `--voices`, `--vocab` | Kokoro model, voice archive and optional vocabulary override |
| `--language`, `--voice` | `us` / `gb` and matching English voice |
| `--provider`, `--device` | `cpu` / `cuda`, nonnegative CUDA device ID |
| `--threads` | Kokoro intra-op threads, default 1; inter-op/frontend fixed at 1 |
| `--speed`, `--trim` | Finite speed 0.5–2.0 (default 1), trim leading/trailing audio more than 60 dB below the peak (default true) |
| `--report` | Optional new JSON synthesis report path |
| `--fallback` | Pronunciation fallback for unknown words: `neural` (default) or `espeak` |
| `--espeak` | eSpeak-ng executable path, used with `--fallback espeak` |
| `--normalize`, `--markdown` | Read numbers, dates, URLs and the like as words; read markdown as prose (both default true) |
| `--config` | YAML config file; see [Configuration](#configuration) |

`--fallback neural` uses the built-in neural models and needs no eSpeak installation.
`--fallback espeak` needs an installed eSpeak-ng (GPL-3.0-or-later), which the
binary runs as a separate program. Both use ONNX Runtime for part-of-speech
tagging. Frontend inference always uses CPU, independently of Kokoro's provider.

`doctor` reports `runtime_library` (the requested selector) and
`runtime_version` (the actual loaded native version). Its build metadata records
the resolved Go binding version separately. An explicit runtime file path is
fingerprinted; a loader name is not incorrectly hashed as a working-directory
file. Synthesis/bench initialization reports also record `runtime_version`.

`bench` measures a separate first request, three warm-ups and 30 warm requests
by default. It records runtime/frontend/voice/vocabulary/model initialization,
preprocessing, preparation, synchronous inference/output retrieval, trim/assembly,
WAV close, audio duration and RTF. Go's two Kokoro loads are included. Profiling,
downloads and report serialization are outside generation timing; no fsync or
cold-disk-cache claim is made. `--warmup 0 --repeat 0` records first only.
`--controlled FILE` accepts prepared Kokoro chunks and bypasses the frontend;
tensor token/style/speed values are used verbatim, so runs can be compared
without frontend changes affecting the input.

## Text preparation

Before pronunciation, text goes through two steps from
[go-ttsnorm](https://github.com/androiddrew/go-ttsnorm), shared with
[gokittentts](https://github.com/androiddrew/gokittentts):

- **markdown** (`--markdown`): headings, list items and table rows become
  sentences; markup, code blocks and link targets are dropped; emoji are removed.
- **normalize** (`--normalize`): clock times (`3:05 p.m.` → "three oh five PM"),
  dates (`May 5, 2026`), money, percents, ordinals, ranges (`pages 31-35`), units
  (`2.5 kg`, `3 GB`), versions, titles (`Dr.`), URLs and emails become words.

Both keep inline pronunciation overrides such as `[Kubernetes](/kˌubəɹnˈɛtiz/)`.
`phonemize --json` shows the prepared text as `prepared_text`. The integration
test `TestNormalizeCorpus` reads go-ttsnorm's 476-input corpus through the
frontend: every input pronounces completely, and its phonemes equal those of the
reference reading or are a reviewed difference in
`testdata/normalize_differences.yaml`. Without these steps, 141 inputs matched the
reference reading and 14 failed to pronounce; with them, 410 match, none fail, and
the other 66 are reviewed differences (mostly letter names Misaki reads better).

## Server

`serve` provides an OpenAI-compatible API, adapted from gokittentts:

```bash
bin/kokoro-run serve --listen 127.0.0.1:8880
curl http://localhost:8880/v1/audio/speech -H 'Content-Type: application/json' \
  -d '{"model":"tts-1","input":"Meet me at 3:05 p.m.","voice":"alloy","response_format":"mp3"}' -o hello.mp3
```

| Endpoint | |
|---|---|
| `POST /v1/audio/speech` | OpenAI's fields: `model` (`kokoro-v1.0`, or `tts-1`, `tts-1-hd`, `gpt-4o-mini-tts`), `input`, `voice` (a Kokoro voice, or an OpenAI name mapped by `server.voices`), `response_format` (`mp3` default, `wav`, `pcm`, and with ffmpeg `opus`, `aac`, `flac`), `speed` (0.25–4, clamped to `server.speed`), `stream_format` (`audio` or `sse`). `instructions` is ignored. Also `normalize` and `markdown` |
| `GET /v1/models`, `GET /v1/voices` | The model and aliases; the English voices and the OpenAI voice map |
| `GET /healthz`, `GET /readyz` | Liveness; readiness once a replica is loaded |
| `GET /metrics` | Prometheus: `kokoro_requests_total`, `kokoro_rtf`, `kokoro_time_to_first_audio_seconds`, `kokoro_synthesis_seconds`, `kokoro_queue_depth`, `kokoro_replicas_loaded` |

Audio streams sentence by sentence: the first sentence's audio is sent while the
rest are synthesized, with `server.chunk_gap` (120 ms) of silence between
sentences. Errors before any audio are OpenAI-style JSON; a failure after audio
has started cuts the response off. `server.replicas` pipelines (each holding its
own Kokoro session, about 330 MB) run at once, up to `server.max_queue` requests
wait, and the rest get 429. `server.limits.request_timeout` covers waiting and
synthesis. Each request logs one line with its RTF and time to first audio.

Unknown words that the fallback can't fully pronounce are spoken as far as known
and logged as `pronunciation_diagnostics`; `server.strict_pronunciation: true`
answers 422 instead. With `KOKORO_RUN_API_KEY` set, `/v1/` requires
`Authorization: Bearer <key>`; serving beyond loopback without one logs a warning.
The server preloads and warms every replica before listening
(`server.preload: false` listens at once and loads in the background).

On an RTX 4090 with CUDA, a two-sentence request starts streaming in about
0.15 s. On a 12-thread CPU with `threads: 6` it takes about 1.4 s; use
`threads` and `replicas` to trade latency against throughput.

## Configuration

Every command reads one set of settings, each layer overriding the one before:

| Layer | Example |
|---|---|
| Built-in defaults | `kokoro-run config` with nothing set |
| YAML file: `--config FILE` or `KOKORO_RUN_CONFIG` | `voice: af_bella` |
| Environment: `KOKORO_RUN_` + the setting's path in upper case | `KOKORO_RUN_SERVER_LISTEN=:8880` |
| Flags given on the command line | `--listen :8880` |

`server.listen` is `KOKORO_RUN_SERVER_LISTEN`, `server.limits.request_timeout` is
`KOKORO_RUN_SERVER_LIMITS_REQUEST_TIMEOUT`, and maps take `key=value,key=value`
(`KOKORO_RUN_SERVER_VOICES=alloy=af_bella,echo=am_adam`). `KOKORO_RUN_ASSETS` and
`KOKORO_RUN_ORT_LIBRARY` keep working as before. The API key is read only from
`KOKORO_RUN_API_KEY` or the file named by `server.api_key_file`, never from the
config itself. Unknown keys in a file are an error. `kokoro-run config` prints the
effective settings as YAML, marking each value's source; it is a valid starting
config file. [`docker/config.yaml`](docker/config.yaml) documents every setting.

## Docker

```bash
make image-cpu                      # kokoro-run:cpu, linux/amd64 and arm64; bakes the model
docker run -p 8880:8880 -v kokoro-assets:/var/lib/kokoro-run/assets kokoro-run:cpu

make image-cuda                     # kokoro-run:cuda, linux/amd64, CUDA 12.8 and cuDNN 9
docker run --gpus all -p 8880:8880 kokoro-run:cuda
```

`make image-cpu` builds each platform with `docker buildx` as
`kokoro-run:cpu-<arch>` and tags the host's as `kokoro-run:cpu`; another platform
needs QEMU `binfmt` support (`IMAGE_PLATFORMS=linux/amd64` builds one). Build
arguments, as make variables:

| Variable | Default | |
|---|---|---|
| `BAKE_MODELS` | `1` | `0` builds a slim image; run `kokoro-run pull` once into its assets volume |
| `WITH_FFMPEG` | `1` | ffmpeg for `opus`, `aac` and `flac` |
| `WITH_ESPEAK` | `0` | `1` installs espeak-ng (GPL-3.0-or-later) for `fallback: espeak` |

The images run as uid 10001, read [`docker/config.yaml`](docker/config.yaml) (CPU,
4 threads) or [`docker/config.cuda.yaml`](docker/config.cuda.yaml) (`provider:
cuda`), listen on `:8880` and report health from `/readyz`. Override any setting
with its environment variable, for example `-e KOKORO_RUN_SERVER_REPLICAS=2
-e KOKORO_RUN_API_KEY=...`, or mount your own config over
`/etc/kokoro-run/config.yaml`. A new named volume at `/var/lib/kokoro-run/assets`
starts as a copy of the baked model; a bind mount must be writable by uid 10001
for `pull`. Without a GPU (no `--gpus all`), the CUDA image exits with "no NVIDIA
GPU device is visible" rather than falling back to the CPU. Other commands run in the image too:
`docker run --rm kokoro-run:cuda doctor --provider cuda`. Licenses are under
`/usr/share/doc/kokoro-run/`.

### Jetson Orin (JetPack 7.2)

Microsoft publishes no aarch64 CUDA build of ONNX Runtime, so the Orin image
uses a base image that compiles ONNX Runtime 1.23.0 for the Orin's GPU (sm_87)
against JetPack 7.2's CUDA 13.2 and cuDNN 9. Build both on the Orin; the base
takes about 4 hours and is built once:

```bash
make image-jetson-orin-onnxruntime JETSON_PARALLEL=3   # onnxruntime-jetson-orin:1.23.0-cuda13.2
make image-jetson-orin                                  # kokoro-run:jetson-orin, minutes
docker run --rm --runtime nvidia kokoro-run:jetson-orin doctor --provider cuda
docker run --runtime nvidia -p 8880:8880 kokoro-run:jetson-orin
```

Each compile job can use 5-10 GB of memory: keep `JETSON_PARALLEL=3` with swap on
a 16 GB Orin, and raise it on a 64 GB AGX Orin. The files are in
[`docker/jetson-orin/`](docker/jetson-orin/); the recipe follows
[straga/jetson-jp7-onnxruntime](https://github.com/straga/jetson-jp7-onnxruntime).
Older JetPack releases (such as JetPack 5 on Xavier) aren't supported by this image.

## Development

```bash
go vet ./...
go test -race ./...
```

The tests in `integration/` (runtime lifetimes, streaming, and the normalization
corpus) use `KOKORO_TEST_ASSETS` and `KOKORO_TEST_ORT`. Without them, native tests
explicitly skip.

CI (`.github/workflows/ci.yml`) runs these tests with ONNX Runtime 1.22.0 and the
Kokoro files, plus `doctor` and `synth` with both fallbacks on CPU. Pushing a `v*`
tag runs `.github/workflows/release.yml`, which builds amd64 and arm64 binaries
natively in `golang:1.27-bookworm`, smoke-tests each one, and publishes the
archives and `SHA256SUMS` as a GitHub release.

## Known limits

This is an English frontend with explicit diagnostics, not universal pronunciation
accuracy. Names, invented words and unusual tokens (`1e10`, `abc123`) can need
spelled-out text or overrides; URLs and emails are spelled letter by letter. Neural unresolved spans are limited to 62 code points / 21 generated
tokens. Incomplete pronunciations fail synthesis; only `phonemize --allow-truncated`
offers an explicit diagnostic opt-in. Unknown Kokoro phonemes fail.

Chunk size is effectively 509 phones. In `synth`, chunks retain phones and
concatenate after trimming without added pauses/crossfades; forced splits can
affect prosody. The server synthesizes sentence by sentence, with
`server.chunk_gap` between them. Some
model output clips: the CLI warns and PCM16 saturates, without a hidden limiter.
`synth` retains whole requests and audio in memory; the server streams. A native
Run is not interruptible, so a cancelled request stops after its current sentence.

Original code is [Apache-2.0](LICENSE). See [THIRD_PARTY.md](THIRD_PARTY.md) for
library/model attribution and the eSpeak variant's external GPL dependency.

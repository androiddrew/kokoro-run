# kokoro-run

A local **US/UK English text-to-speech CLI** in Go, using Kokoro and ONNX Runtime.
Runs on CPU or CUDA with a neural or eSpeak pronunciation fallback, chosen per run.
It is a single Go binary: no Python or other interpreter is needed at run time.

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

Install the bundle described in [ASSETS.md](ASSETS.md), then configure:

```bash
export KOKORO_RUN_ASSETS=/absolute/path/to/runtime-assets
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
./synth-all-voices.sh
```

`synth`, `phonemize` and `bench` accept `--text`, `--file`, or stdin. The English
voice list includes `a*` US and `b*` UK voices. Defaults are `af_heart` / `bf_emma`;
voice/dialect mismatches fail. `synth` writes 24 kHz mono PCM16. Inline phoneme
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

## Development

```bash
go vet ./...
go test -race ./...
```

Combined runtime-lifetime tests in `integration/` use `KOKORO_TEST_ASSETS` and
`KOKORO_TEST_ORT`. Without them, native tests explicitly skip.

CI (`.github/workflows/ci.yml`) runs these tests with ONNX Runtime 1.22.0 and the
Kokoro files, plus `doctor` and `synth` with both fallbacks on CPU. Pushing a `v*`
tag runs `.github/workflows/release.yml`, which builds amd64 and arm64 binaries
natively in `golang:1.27-bookworm`, smoke-tests each one, and publishes the
archives and `SHA256SUMS` as a GitHub release.

## Known limits

This is an English frontend with explicit diagnostics, not universal pronunciation
accuracy. Clock times such as `10:30`, names and URLs can need spelled-out text or
overrides. Neural unresolved spans are limited to 62 code points / 21 generated
tokens. Incomplete pronunciations fail synthesis; only `phonemize --allow-truncated`
offers an explicit diagnostic opt-in. Unknown Kokoro phonemes fail.

Chunk size is effectively 509 phones. Chunks retain phones and concatenate after
trimming without added pauses/crossfades; forced splits can affect prosody. Some
model output clips: the CLI warns and PCM16 saturates, without a hidden limiter.
Whole requests/audio are retained in memory and native Run is not interruptible.

Original code is [Apache-2.0](LICENSE). See [THIRD_PARTY.md](THIRD_PARTY.md) for
library/model attribution and the eSpeak variant's external GPL dependency.

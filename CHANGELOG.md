# Changelog

All notable changes to kokoro-run are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/). Each release's section is published
as its GitHub release notes.

## [Unreleased]

### Changed

- go-kokoro, go-g2p and ortenv are upgraded to v0.2.0. ONNX Runtime now stays
  loaded from the first pipeline load until the process exits, rather than
  being unloaded when the last model closes.

### Fixed

- `serve` without preload could unload and reload ONNX Runtime in one process
  when the first replicas failed to load after the runtime had started.
  Reloading the runtime can crash the CUDA provider
  ([ortenv#2](https://github.com/androiddrew/ortenv/issues/2)); the runtime is
  now loaded once and kept.

## [0.2.0] - 2026-10-04

### Added

- **OpenAI-compatible HTTP server**, `kokoro-run serve`: `POST /v1/audio/speech`,
  `GET /v1/models` and `GET /v1/voices`, with `mp3`, `wav` and `pcm` output (and
  `opus`, `aac` and `flac` with ffmpeg), sentence-by-sentence streaming, SSE
  (`stream_format: sse`), OpenAI voice names mapped to Kokoro voices, optional
  bearer-key auth (`KOKORO_RUN_API_KEY`), parallel replicas with a bounded queue
  (429 when full), a request timeout, `/healthz`, `/readyz` and Prometheus
  `/metrics`. Each request logs its real-time factor and time to first audio.
- **Layered configuration** for every command: built-in defaults, then a YAML file
  (`--config` or `KOKORO_RUN_CONFIG`), then `KOKORO_RUN_*` environment variables,
  then flags. Every setting has an environment variable named from its YAML path,
  such as `KOKORO_RUN_SERVER_LISTEN`.
- **Text preparation** before pronunciation, from the shared
  [go-ttsnorm](https://github.com/androiddrew/go-ttsnorm) module: clock times,
  dates, money, percents, ordinals, ranges, units, versions, titles, URLs and
  emails are read as words, and markdown is read as prose. Inline pronunciation
  overrides pass through unchanged. On go-ttsnorm's 476-input corpus, every input
  now pronounces completely (14 failed before), and 410 match the reference
  reading (141 before). `--normalize` and `--markdown` turn the steps on or off.
- **Docker images**, with ONNX Runtime and the Kokoro model built in:
  - CPU, for linux/amd64 and linux/arm64 (`make image-cpu`).
  - CUDA 12.8 for x86-64 NVIDIA GPUs (`make image-cuda`).
  - Jetson Orin on JetPack 7.2, with ONNX Runtime 1.23 built for CUDA 13.2
    (`make image-jetson-orin`).
  - Jetson Xavier on JetPack 5, with ONNX Runtime 1.22 built for CUDA 12.2 through
    `cuda-compat` (`make image-jetson-xavier`).

  Build options bake the model or leave it out (`BAKE_MODELS`) and add ffmpeg or
  eSpeak-ng (`WITH_FFMPEG`, `WITH_ESPEAK`). See "Getting started with Docker" in
  the README.
- `kokoro-run pull` downloads the Kokoro model and voices and checks their SHA-256.
- `kokoro-run config` prints the effective settings and where each came from.
- `phonemize --json` and `synth --report` include the prepared text.
- The Kokoro-82M model card (`third_party/kokoro-82M-MODEL_CARD.md`) ships with
  the source, the release archives and the images.

### Changed

- `synth`, `phonemize` and `bench` now normalize text and read markdown by
  default, so the same input can produce different audio than in 0.1.0. Pass
  `--normalize=false --markdown=false` for the 0.1.0 behavior.
- A `--voice` given without `--language` now selects its own dialect (`b*` voices
  are UK English) instead of failing against the default `us`. An explicit
  mismatch still fails.
- `--ort` and the `--model`/`--voices` paths no longer show environment-dependent
  defaults in `--help`; the environment variables still apply, through the new
  configuration layers.
- `synth-all-voices.sh` moved to `scripts/synth-all-voices.sh`.
- New dependencies are listed in THIRD_PARTY.md. The MP3 encoder, shine-mp3, is
  LGPL-2.0; see THIRD_PARTY.md for how kokoro-run meets its terms.

### Fixed

- With `--provider cuda` and no visible NVIDIA GPU (for example a container run
  without `--gpus all`), kokoro-run now exits with "no NVIDIA GPU device is
  visible" instead of crashing inside ONNX Runtime.

## [0.1.0] - 2026-10-02

### Added

- First release: a US/UK English text-to-speech CLI using Kokoro v1.0 and ONNX
  Runtime, as one Go binary. Commands `synth`, `phonemize`, `voices`, `doctor`
  and `bench`, on CPU or CUDA, with a neural (default) or eSpeak-ng pronunciation
  fallback.
- Release archives for linux/amd64 and linux/arm64 with SHA256SUMS.

[Unreleased]: https://github.com/androiddrew/kokoro-run/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/androiddrew/kokoro-run/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/androiddrew/kokoro-run/releases/tag/v0.1.0

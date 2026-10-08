# Attribution and distribution

Original application code is Apache-2.0 (LICENSE/NOTICE). License and notice
files for everything below are under `third_party/`, and every binary release
archive includes them together with LICENSE, NOTICE and this file.

## Go modules compiled into the binary

| Module | Version | Terms |
|---|---|---|
| github.com/androiddrew/go-g2p | v0.2.0 | Apache-2.0; `third_party/go-g2p-NOTICE` |
| github.com/androiddrew/go-kokoro | v0.2.0 | Apache-2.0; `third_party/go-kokoro-NOTICE` |
| github.com/androiddrew/go-ttsnorm | v0.1.0 | Apache-2.0; ports KittenTTS's text preprocessing (Apache-2.0); `third_party/go-ttsnorm-NOTICE` |
| github.com/androiddrew/ortenv | v0.2.0 | MIT, Copyright (c) 2026 Drew Bednar; `ortenv.txt`, `third_party/ortenv-NOTICE` |
| github.com/spf13/cobra | v1.10.2 | Apache-2.0; `cobra.txt` |
| github.com/spf13/pflag | v1.0.10 | BSD-3-Clause; `pflag.txt` |
| github.com/yalue/onnxruntime_go | v1.22.0 | MIT; `onnxruntime_go.txt` |
| github.com/dlclark/regexp2 | v1.12.0 | MIT plus upstream attributions; `regexp2.txt`, `regexp2-ATTRIB.txt` |
| golang.org/x/text | v0.41.0 | BSD-3-Clause; `golang-x-text.txt` |
| github.com/inconshreveable/mousetrap | v1.1.0 | Apache-2.0; `mousetrap.txt` (Windows builds only) |
| github.com/braheezy/shine-mp3 | v0.2.0 | LGPL-2.0; `shine-mp3.txt` (see below) |
| github.com/yuin/goldmark | v1.8.6 | MIT; `goldmark.txt` |
| go.yaml.in/yaml/v3 | v3.0.5 | MIT and Apache-2.0; `yaml-v3.txt`, `yaml-v3-NOTICE.txt` |
| github.com/prometheus/client_golang | v1.24.1 | Apache-2.0; `prometheus-client_golang.txt`, `prometheus-client_golang-NOTICE.txt` |
| github.com/prometheus/client_model | v0.6.2 | Apache-2.0; `prometheus-client_model.txt`, `prometheus-client_model-NOTICE.txt` |
| github.com/prometheus/common | v0.70.1 | Apache-2.0; `prometheus-common.txt`, `prometheus-common-NOTICE.txt` |
| github.com/prometheus/procfs | v0.21.1 | Apache-2.0; `prometheus-procfs.txt`, `prometheus-procfs-NOTICE.txt` |
| github.com/beorn7/perks | v1.0.1 | MIT; `perks.txt` |
| github.com/cespare/xxhash/v2 | v2.3.0 | MIT; `xxhash.txt` |
| github.com/munnerz/goautoneg | v0.0.0-20191010083416-a7dc8b61c822 | BSD-3-Clause; `goautoneg.txt` |
| golang.org/x/sys | v0.47.0 | BSD-3-Clause; `golang-x-sys.txt` |
| google.golang.org/protobuf | v1.36.11 | BSD-3-Clause; `protobuf.txt` |

**shine-mp3** is a pure Go port of the shine fixed-point MP3 encoder, used for the
server's `mp3` format. It is LGPL-2.0. Go links statically, so to meet the LGPL's
relinking terms kokoro-run's complete source is public and anyone can rebuild the
binary with a modified shine-mp3 (a `replace` directive in `go.mod`, then
`make build`). Its source is at https://github.com/braheezy/shine-mp3.

Files named without a directory are in `third_party/licenses/`.

## Data and models built into the binary

go-g2p and go-kokoro embed these files, so every kokoro-run binary contains them:

| Content | Source | Terms |
|---|---|---|
| Pronunciation dictionaries | hexgrad/misaki `fba1236595f2d2bf21d414ba6e57d25256afada3`, unchanged | Apache-2.0; `misaki.txt` |
| Tokenizer data and POS model | spaCy en_core_web_sm 3.8.0, converted to JSON and ONNX | MIT plus training-source notices; `en-core-web-sm-*.txt`, `spacy-LICENSE.txt`, `thinc-LICENSE.txt` |
| Neural fallback models (US/UK) | PeterReid/graphemes_to_phonemes_en_us and _en_gb; modified: split into encoder/decoder and converted to ONNX | Apache-2.0; changes described in `third_party/go-g2p-NOTICE` |
| Unicode digit table | Unicode 15.0 | `unicode.txt` |
| Kokoro vocabulary | hexgrad/Kokoro-82M `config.json`, commit `f3ff3571791e39611d31c381e3a41a3af07b4987` | Apache-2.0; `third_party/go-kokoro-NOTICE` |

Code adapted inside the libraries keeps its notices: Misaki (Apache-2.0), spaCy
and Thinc (MIT), kokoro-onnx (MIT, `kokoro-onnx.txt`) and librosa's trimming
algorithm (ISC, `librosa-trim.txt`).

## Not included

- **Kokoro v1.0 model and voices.** Users download them separately (see
  ASSETS.md). Their model card declares Apache-2.0; preserve its StyleTTS 2,
  ISTFTNet and training-data acknowledgments, including Koniwa `tnc` (CC BY 3.0)
  and SIWIS (CC BY 4.0), with any redistributed copies.
- **ONNX Runtime and CUDA/cuDNN.** Users install these themselves; neither the
  repository nor the release archives ship them or their license bundles. The
  Docker images do; see below.
- **eSpeak-ng.** `--fallback espeak` runs an installed eSpeak-ng executable as a
  separate program. The engine and its data remain GPL-3.0-or-later, including
  their source-distribution obligations. The binary contains no eSpeak code and
  does not link libespeak-ng; runs with the default `--fallback neural` never
  start eSpeak, and eSpeak need not be installed for them.

## Docker images

The images in `docker/` add these to the binary, with their terms under
`/usr/share/doc/kokoro-run/` and each Debian or Ubuntu package's own
`/usr/share/doc/<package>/copyright`:

- **ONNX Runtime 1.22.0** (MIT), with its `LICENSE` and `ThirdPartyNotices.txt`
  under `onnxruntime/`. The CUDA image uses the CUDA 12 build and its CUDA
  provider libraries.
- **The Kokoro v1.0 model and voices**, unless built with `BAKE_MODELS=0`. The
  Kokoro-82M model card (`third_party/kokoro-82M-MODEL_CARD.md`, from
  hexgrad/Kokoro-82M at commit `f3ff3571791e39611d31c381e3a41a3af07b4987`) carries
  its Apache-2.0 license, the CC BY 3.0 (Koniwa `tnc`) and CC BY 4.0 (SIWIS)
  training-data attributions and its acknowledgements. Every image has it under
  `third_party/`, and images that bake the model also have it beside the model as
  `/var/lib/kokoro-run/assets/kokoro/MODEL_CARD.md`.
- **ffmpeg** (Debian's or Ubuntu's package, built with `--enable-gpl`), unless
  built with `WITH_FFMPEG=0`. It runs as a separate process for the `opus`, `aac`
  and `flac` formats and is never linked.
- **eSpeak-ng** (GPL-3.0-or-later), only when built with `WITH_ESPEAK=1`, run as
  a separate process as described above.
- **The Jetson images** carry NVIDIA's CUDA runtime libraries (cuBLAS, cuFFT,
  cuRAND) and cuDNN 9, from NVIDIA's Jetson apt repository (Orin) or its redist
  archives (Xavier, with the `cuda-compat` driver), under the CUDA and cuDNN
  license agreements, whose texts are kept with the libraries. Their ONNX Runtime
  is built from source in `docker/jetson-*/Dockerfile.onnxruntime`; its `LICENSE`
  and `ThirdPartyNotices.txt` are in `/usr/share/doc/onnxruntime/`.
- **The CUDA image's base**, `nvidia/cuda:12.8.1-cudnn-runtime-ubuntu24.04`, under
  NVIDIA's container license, linked at `NGC-DL-CONTAINER-LICENSE`.

Anyone redistributing an image redistributes these components and takes on their
obligations, such as offering the source of the GPL packages, which Debian and
Ubuntu publish for every package version.

# Attribution and distribution

Original application code is Apache-2.0 (LICENSE/NOTICE). License and notice
files for everything below are under `third_party/`, and every binary release
archive includes them together with LICENSE, NOTICE and this file.

## Go modules compiled into the binary

| Module | Version | Terms |
|---|---|---|
| github.com/androiddrew/go-g2p | v0.1.0 | Apache-2.0; `third_party/go-g2p-NOTICE` |
| github.com/androiddrew/go-kokoro | v0.1.0 | Apache-2.0; `third_party/go-kokoro-NOTICE` |
| github.com/androiddrew/ortenv | v0.1.0 | MIT, Copyright (c) 2026 Drew Bednar; `ortenv.txt`, `third_party/ortenv-NOTICE` |
| github.com/spf13/cobra | v1.10.2 | Apache-2.0; `cobra.txt` |
| github.com/spf13/pflag | v1.0.10 | BSD-3-Clause; `pflag.txt` |
| github.com/yalue/onnxruntime_go | v1.22.0 | MIT; `onnxruntime_go.txt` |
| github.com/dlclark/regexp2 | v1.12.0 | MIT plus upstream attributions; `regexp2.txt`, `regexp2-ATTRIB.txt` |
| golang.org/x/text | v0.41.0 | BSD-3-Clause; `golang-x-text.txt` |
| github.com/inconshreveable/mousetrap | v1.1.0 | Apache-2.0; `mousetrap.txt` (Windows builds only) |

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
  repository nor the release archives ship them or their license bundles.
- **eSpeak-ng.** `--fallback espeak` runs an installed eSpeak-ng executable as a
  separate program. The engine and its data remain GPL-3.0-or-later, including
  their source-distribution obligations. The binary contains no eSpeak code and
  does not link libespeak-ng; runs with the default `--fallback neural` never
  start eSpeak, and eSpeak need not be installed for them.

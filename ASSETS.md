# Model files and standalone deployment

The pronunciation frontend's dictionaries and models are built into the binary
(from go-g2p). Only the Kokoro model and voices are separate downloads. Set
`KOKORO_RUN_ASSETS` to a directory with this layout:

```text
kokoro/
  kokoro-v1.0.onnx   # 325.5 MB
  voices-v1.0.bin    # 28.2 MB
```

Both files come from the kokoro-onnx `model-files-v1.0` release:

- https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/kokoro-v1.0.onnx
- https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/voices-v1.0.bin

`internal/assets/manifest.json` records their SHA-256, sizes and URLs; go-kokoro's
`SHA256SUMS` lets you check them with `sha256sum -c`. The Kokoro vocabulary is
built in as well. `--data` and `--frontend-models` replace the built-in frontend
files only when you need different ones. Native runtimes are a separate user
installation, not files inside `KOKORO_RUN_ASSETS`.

Native requirements: the runtime must provide the C API requested by the resolved
Go binding (currently API 22). Select it with `KOKORO_RUN_ORT_LIBRARY` / `--ort`, either
an explicit path or an OS-loader name such as `libonnxruntime.so`. System/package-
manager installs are supported. The runtime's own packaging defines the placement
of provider libraries and CUDA/cuDNN dependencies; install/configure them through
your chosen method. The application preserves `LD_LIBRARY_PATH`. Use `ldd` and
`doctor` for diagnostics. ORT 1.22.0 on Linux/amd64, with CUDA 12.8/cuDNN 9.8 for
GPU use, is the existing tested baseline rather than a hard-coded version lock.

`--fallback espeak` additionally needs the system executable and data (tested
Ubuntu eSpeak-ng `1.51+dfsg-12build1`). The default `--fallback neural` has no
eSpeak dependency.

Keep the Kokoro model card and licenses with any redistributed copies; see
go-kokoro's ASSETS.md and THIRD_PARTY.md.

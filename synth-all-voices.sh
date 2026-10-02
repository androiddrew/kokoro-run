#!/usr/bin/env bash
# Generate one sample per English voice supported by the CLI, without Python.
set -euo pipefail

usage() {
    printf '%s\n' \
        'Usage: ./synth-all-voices.sh [NEW_OUTPUT_DIRECTORY] [TEXT]' \
        '' \
        'Defaults: a new artifacts/voice-samples.XXXXXX directory; a short sample sentence.' \
        'Relative output paths are relative to your current directory.' \
        '' \
        'Environment:' \
        '  PROVIDER=cpu|cuda           Default: cpu' \
        '  FALLBACK=neural|espeak      Default: neural' \
        '  BINARY=/path/to/binary      Default: bin/kokoro-run beside this script' \
        '  KOKORO_RUN_ASSETS=...       Default: assets beside this script' \
        '  KOKORO_RUN_ORT_LIBRARY=...  Library path or loader name; default: libonnxruntime.so' \
        '' \
        'Example: PROVIDER=cuda ./synth-all-voices.sh artifacts/all-voices "Hello, world!"'
}

if [[ ${1:-} == --help || ${1:-} == -h ]]; then
    usage
    exit 0
fi
if (( $# > 2 )); then
    usage >&2
    exit 2
fi

root=$(dirname -- "$(realpath -- "${BASH_SOURCE[0]}")")
binary=${BINARY:-"$root/bin/kokoro-run"}
provider=${PROVIDER:-cpu}
fallback=${FALLBACK:-neural}
bundle=${KOKORO_RUN_ASSETS:-"$root/assets"}
ort=${KOKORO_RUN_ORT_LIBRARY:-libonnxruntime.so}
archive="$bundle/kokoro/voices-v1.0.bin"
text=${2-"Hello, world! This is a sample of my voice."}

if [[ $provider != cpu && $provider != cuda ]]; then
    printf 'PROVIDER must be cpu or cuda, got: %s\n' "$provider" >&2
    exit 2
fi
if [[ ! -x $binary ]]; then
    printf 'Executable not found: %s\nBuild the CLI first; see README.md.\n' "$binary" >&2
    exit 1
fi
if [[ $ort == */* && ! -f $ort ]]; then
    printf 'ORT library not found: %s\nSet KOKORO_RUN_ORT_LIBRARY or follow ASSETS.md.\n' "$ort" >&2
    exit 1
fi
if [[ ! $text =~ [^[:space:]] ]]; then
    printf 'Sample text must not be empty.\n' >&2
    exit 2
fi

# The user's runtime installation supplies CUDA dependencies and loader paths.

# Capture the command first so a failed listing cannot be hidden by process substitution.
voice_list=$("$binary" voices --voices "$archive")
if [[ -z $voice_list ]]; then
    printf 'No supported English voices found in %s\n' "$archive" >&2
    exit 1
fi
mapfile -t voices <<< "$voice_list"

if [[ -n ${1:-} ]]; then
    output=$1
    mkdir -- "$output" # Require a new directory; preserve existing samples.
else
    mkdir -p -- "$root/artifacts"
    output=$(mktemp -d "$root/artifacts/voice-samples.XXXXXX")
fi

printf 'Writing %d English voice samples to %s (%s)\n' "${#voices[@]}" "$output" "$provider"
failed=()
index=0
for voice in "${voices[@]}"; do
    case "$voice" in
        a*) language=us ;;
        b*) language=gb ;;
        *) printf 'Unsupported voice: %s\n' "$voice" >&2; exit 1 ;;
    esac
    index=$((index + 1))
    printf '\n[%d/%d] %s (%s)\n' "$index" "${#voices[@]}" "$voice" "$language"
    if ! "$binary" synth \
        --ort "$ort" \
        --model "$bundle/kokoro/kokoro-v1.0.onnx" \
        --voices "$archive" \
        --fallback "$fallback" --provider "$provider" --language "$language" --voice "$voice" \
        --text "$text" --output "$output/$voice.wav"; then
        failed+=("$voice")
    fi
done

if (( ${#failed[@]} )); then
    printf '\nSynthesis failed for: %s\nCompleted samples are in: %s\n' "${failed[*]}" "$output" >&2
    exit 1
fi
printf '\nFinished: %d WAV files in %s\n' "${#voices[@]}" "$output"
